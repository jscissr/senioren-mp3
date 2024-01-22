package main

import (
	"log"

	"github.com/stianeikeland/go-rpio/v4"
)

type Pin = rpio.Pin

type ButtonEvent struct {
	Pin       Pin
	LongPress bool
}

const (
	NilPin     = Pin(0)
	invalidPin = Pin(1)
)

var (
	lastButtonDown      = false
	lastButtonDownTicks = int64(0)
	lastButton          = NilPin
)

func InitButtons() {
	err := rpio.Open()
	if err != nil {
		log.Fatal(err)
	}
	for _, pin := range allButtonPins {
		pin.Input()
		pin.PullUp()
	}
}

func ReadButtons() (event ButtonEvent) {
	currentButton := NilPin
	for _, pin := range allButtonPins {
		if pin.Read() == 0 {
			if currentButton == NilPin {
				currentButton = pin
			} else {
				currentButton = invalidPin
				break
			}
		}
	}
	event = ButtonEvent{Pin: NilPin, LongPress: false}
	lastButtonDownTicks += 1
	if currentButton == NilPin {
		lastButtonDown = false
		return
	}
	if lastButton == invalidPin && lastButtonDown == true {
		currentButton = invalidPin
	}
	if currentButton != invalidPin && ((!lastButtonDown && (currentButton != lastButton || lastButtonDownTicks > 3)) ||
		(lastButtonDown && lastButtonDownTicks > buttonLongPressTicks)) {
		event.Pin = currentButton
		event.LongPress = lastButtonDown
	}
	lastButton = currentButton
	if !lastButtonDown {
		lastButtonDownTicks = 0
		lastButtonDown = true
	}
	return
}
