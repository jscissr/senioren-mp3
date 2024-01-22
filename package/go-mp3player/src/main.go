package main

/*
#include <stdlib.h>
#include <gst/gst.h>

#cgo pkg-config: gstreamer-1.0
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"io/ioutil"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ziutek/glib"
	"github.com/ziutek/gst"
)

const (
	mountDir            = "/run/media"
	loadMax             = 12
	forwardBackwardTime = 15 * time.Second
	storeInterval       = 5 * time.Second
)

var (
	audioExts = []string{".wav", ".mp3", ".ogg", ".opus"}
)

type audioFile struct {
	uri      string
	filesize int64
}

type StorageData struct {
	Volume       int64
	LastUri      string
	LastFilesize int64
	LastPosition time.Duration
}

const (
	playPausePin         = Pin(15)
	backwardPin          = Pin(14)
	forwardPin           = Pin(18)
	volUpPin             = Pin(23)
	volDownPin           = Pin(24)
	buttonPollTime       = 20 * time.Millisecond
	buttonLongPressTicks = int64(600 * time.Millisecond / buttonPollTime)
)

var (
	selectPins    = []Pin{2, 3, 4, 17, 27, 22, 10, 9, 11, 7, 8, 25}
	allButtonPins = append([]Pin{playPausePin, forwardPin, backwardPin, volUpPin, volDownPin}, selectPins...)
)

var (
	mux           sync.Mutex
	pipeline      *gst.Element
	volume        = int64(5)
	openFiles     []*audioFile
	playing       *audioFile
	paused        bool
	isAtEnd       bool
	duration      time.Duration
	lastPosition  time.Duration
	asyncPosition = time.Duration(-1)
	lastData      StorageData
)

func min(x, y int64) int64 {
	if x < y {
		return x
	}
	return y
}
func max(x, y int64) int64 {
	if x > y {
		return x
	}
	return y
}

func playerC() *C.GstElement {
	return (*C.GstElement)(pipeline.GetPtr())
}

func getPosition() time.Duration {
	if isAtEnd {
		return duration
	}
	var cPos C.int64_t
	ok := C.gst_element_query_position(playerC(), C.GST_FORMAT_TIME, &cPos) != 0
	if !ok {
		fmt.Println("mp3player query position failed")
		return lastPosition
	}
	return time.Duration(cPos)
}

func seek(t time.Duration) {
	if t < 0 {
		t = 0
	}
	fmt.Printf("Seek %v\n", t)
	ok := C.gst_element_seek_simple(playerC(), C.GST_FORMAT_TIME,
		C.GST_SEEK_FLAG_FLUSH|C.GST_SEEK_FLAG_KEY_UNIT, C.int64_t(t)) != 0

	lastPosition = t
	if !ok {
		log.Printf("mp3player seek error")
	}
}

func setVolume(newVolume int64) {
	newVolume = max(newVolume, 1)
	newVolume = min(newVolume, 10)
	volume = newVolume
	fmt.Printf("Volume %d\n", volume)
	expVol := float64(volume) / 10.0
	expVol = (math.Exp(2.0*expVol) - 1.0) / (math.Exp(2.0) - 1.0)
	pipeline.SetProperty("volume", expVol)
}

func readyFile(file *audioFile) {
	playing = file
	pipeline.SetState(gst.STATE_READY)
	pipeline.SetProperty("uri", playing.uri)
	duration = 0
	lastPosition = 0
	isAtEnd = false
	paused = true
	fmt.Printf("Loading %s\n", playing.uri)
}

func playFile(file *audioFile) {
	if file == playing {
		seek(0)
	} else {
		readyFile(file)
	}
	paused = false
	isAtEnd = false
	pipeline.SetState(gst.STATE_PLAYING)
}

func storeState() {
	lastData.Volume = volume
	if playing != nil {
		lastData.LastUri = playing.uri
		lastData.LastFilesize = playing.filesize
		lastData.LastPosition = getPosition()
	}
	b, err := json.Marshal(lastData)
	if err != nil {
		panic(err)
	}
	if err := storeData(b); err != nil {
		fmt.Printf("storeData failed: %v\n", err)
	}
}

func handleButtonEvent(event ButtonEvent) {
	mux.Lock()
	defer mux.Unlock()

	if event.LongPress {
		fmt.Println("Long press (not supported yet)")
		return // TODO
	}
	if event.Pin == playPausePin && playing != nil {
		paused = !paused
		if paused {
			pipeline.SetState(gst.STATE_PAUSED)
		} else {
			if isAtEnd {
				isAtEnd = false
				seek(0)
			}
			pipeline.SetState(gst.STATE_PLAYING)
		}
	}
	if (event.Pin == backwardPin || event.Pin == forwardPin) && playing != nil {
		if isAtEnd {
			if event.Pin == backwardPin {
				isAtEnd = false
				seek(duration - forwardBackwardTime)
				pipeline.SetState(gst.STATE_PLAYING)
				paused = false
			}
			return
		}
		pos := getPosition()
		if event.Pin == backwardPin {
			pos -= forwardBackwardTime
		} else {
			pos += forwardBackwardTime
		}
		seek(pos)
	}
	if event.Pin == volUpPin || event.Pin == volDownPin {
		if event.Pin == volUpPin {
			setVolume(volume + 1)
		} else {
			setVolume(volume - 1)
		}
	}
	for i, selectPin := range selectPins {
		if event.Pin == selectPin && len(openFiles) > i {
			playFile(openFiles[i])
		}
	}
	storeState()
}

func collectFiles(cb func(string, int64) bool) {
	mounts, _ := ioutil.ReadDir(mountDir)
	for _, mountInfo := range mounts {
		if mountInfo.IsDir() {
			mount := filepath.Join(mountDir, mountInfo.Name())
			files, _ := ioutil.ReadDir(mount)
			for _, fileInfo := range files {
				if !fileInfo.IsDir() {
					name := fileInfo.Name()
					if strings.HasPrefix(name, "._") { // macOS metadata files
						continue
					}
					filename := filepath.Join(mount, name)
					if cb(filename, fileInfo.Size()) {
						return
					}
				}
			}
		}
	}
}
func reloadMedia() {
	mux.Lock()
	defer mux.Unlock()

	oldFiles := openFiles
	newFiles := make([]*audioFile, 0, loadMax)

	collectFiles(func(filename string, filesize int64) bool {
		ext := strings.ToLower(filepath.Ext(filename))
		isAudio := false
		for _, aExt := range audioExts {
			if ext == aExt {
				isAudio = true
				break
			}
		}
		if !isAudio {
			return false
		}
		uri, err := gst.FilenameToURI(filename)
		if err != nil {
			return false
		}
		// Check if this file was already loaded
		for i, oldFile := range oldFiles {
			if oldFile.uri == uri {
				newFiles = append(newFiles, oldFile)
				oldFiles[i] = oldFiles[len(oldFiles)-1]
				oldFiles = oldFiles[:len(oldFiles)-1]
				return len(newFiles) == loadMax
			}
		}
		newFiles = append(newFiles, &audioFile{uri: uri, filesize: filesize})
		return len(newFiles) == loadMax
	})

	// close old files
	openFiles = newFiles
	for _, oldFile := range oldFiles {
		if playing == oldFile {
			playing = nil
			pipeline.SetState(gst.STATE_NULL)
		}
	}

	if playing == nil {
		for _, file := range openFiles {
			if file.uri == lastData.LastUri && file.filesize == lastData.LastFilesize {
				readyFile(file)
				pipeline.SetState(gst.STATE_PAUSED)
				asyncPosition = lastData.LastPosition
				lastPosition = lastData.LastPosition
				break
			}
		}
	}

	fmt.Println("mp3player: reloaded media")
	for i, file := range openFiles {
		fmt.Printf("%d. %s\n", i, file.uri)
	}
}

func onMessage(msg *gst.Message) {
	mux.Lock()
	defer mux.Unlock()

	switch msg.GetType() {
	case gst.MESSAGE_ASYNC_DONE:
		// fmt.Print("Gst async done\n")
		var cDur C.int64_t
		ok := C.gst_element_query_duration(playerC(), C.GST_FORMAT_TIME, &cDur) != 0
		if !ok {
			log.Printf("mp3player query duration error")
			return
		}
		duration = time.Duration(cDur)

		if asyncPosition != -1 {
			seek(asyncPosition)
			asyncPosition = -1
		}
	case gst.MESSAGE_EOS:
		isAtEnd = true
		paused = true
		pipeline.SetState(gst.STATE_PAUSED)
		fmt.Print("Gst EOS\n")
	case gst.MESSAGE_ERROR:
		err, debug := msg.ParseError()
		fmt.Printf("Gst Error: %v (%s)\n", err, debug)
	}
}

func main() {
	fmt.Println("mp3player: starting")

	InitButtons()

	pipeline = gst.ElementFactoryMake("playbin", "playbin")
	if pipeline == nil {
		panic("cannot create playbin")
	}
	bus := pipeline.GetBus()
	bus.AddSignalWatch()
	bus.ConnectNoi("message", onMessage, nil)

	setVolume(volume)

	storedData, err := loadData()
	if err != nil {
		fmt.Printf("loadData failed: %v\n", err)
	} else {
		if err := json.Unmarshal(storedData, &lastData); err != nil {
			fmt.Printf("JSON decoding failed: %v\n", err)
		} else {
			setVolume(lastData.Volume)
		}
	}

	mountSignal := make(chan os.Signal, 1)
	// Use SIGWINCH because that is one of the few signals which
	// don't kill the process when the Go runtime didn't have
	// a chance yet to ignore the signal.
	signal.Notify(mountSignal, unix.SIGWINCH)

	reloadMedia()

	go func() {
		glib.NewMainLoop(nil).Run()
	}()

	pollTick := time.Tick(buttonPollTime)
	storeTick := time.Tick(storeInterval)

	// Start main loop
	fmt.Println("mp3player is running")
	for {
		select {
		case <-mountSignal:
			fmt.Println("mp3player: Got mount signal")
			reloadMedia()
		case <-pollTick:
			event := ReadButtons()
			if event.Pin != NilPin {
				handleButtonEvent(event)
			}
		case <-storeTick:
			if !paused {
				storeState()
			}
		}
	}
}
