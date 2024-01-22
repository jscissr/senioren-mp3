package main

/*
#include <linux/fs.h>
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"hash/crc64"
	"os"
	"unsafe"
)

/*
	This module stores and loads configuration data from flash memory.
	The intended usage is to load the configuration once on startup
	and then repeatedly store when the configuration changes.

	The code tries to do wear leveling by not always writing to the same
	address, but instead sequentially writes fixed-size blocks.
	Each block has a sequence number, on startup we look for the block
	with highest sequence number using binary search.
	We also store a hash of the data, because it might get corrupted if
	power is turned off while we write the block.
	There are two large 'erase blocks', whenever one is full we discard
	the other erase block and start writing there.

	We assume that the flash has a controller which does bad block
	management. If we discard the whole flash before writing the
	software, the hope is that the controller will do wear-leveling
	among all unused erase blocks.
	More information: https://lwn.net/Articles/428584/

	Storage format:
	Magic    uint64
	Length   uint64
	Hash     uint64
	Sequence uint64
	Content  []byte
*/

const (
	storeMagic          = uint64(0x464c415348535452) // FLASHSTR
	storeFileName       = "/dev/mmcblk0p3"
	storeBlockSize      = 16 * 1024
	eraseblockSize      = 4 * 1024 * 1024
	blocksPerEraseblock = eraseblockSize / storeBlockSize
	storeContentSize    = storeBlockSize - 4*8
)

var (
	errNoData      = errors.New("load failed: no data stored")
	errTooBig      = errors.New("store failed: data too big")
	crc64Table     = crc64.MakeTable(crc64.ECMA)
	binCoder       = binary.BigEndian
	storeFile      *os.File
	tmpBlock       = make([]byte, storeBlockSize)
	prevSequence   = uint64(0)
	prevContent    = make([]byte, 0, storeContentSize)
	prevBlock      = blocksPerEraseblock - 1
	prevEraseblock = 1
)

// https://github.com/moby/moby/blob/master/pkg/devicemapper/ioctl.go
func ioctlBlkDiscard(fd uintptr, start, length uint64) error {
	args := [2]uint64{start, length}
	if _, _, err := unix.Syscall(unix.SYS_IOCTL, fd, C.BLKDISCARD, uintptr(unsafe.Pointer(&args[0]))); err != 0 {
		return err
	}
	return nil
}

func calcBlockHash(seq uint64, content []byte) uint64 {
	hash := crc64.New(crc64Table)
	// hash.Write ever returns an error
	binary.Write(hash, binary.BigEndian, seq)
	hash.Write(content)
	return hash.Sum64()
}

func readBlockAt(addr int64) (found bool, err error) {
	if _, err := storeFile.ReadAt(tmpBlock, addr); err != nil {
		return false, err
	}

	blockMagic := binCoder.Uint64(tmpBlock[0*8:])
	blockLen := binCoder.Uint64(tmpBlock[1*8:])
	blockHash := binCoder.Uint64(tmpBlock[2*8:])
	blockSequence := binCoder.Uint64(tmpBlock[3*8:])
	if blockMagic != storeMagic ||
		blockLen > storeContentSize ||
		blockSequence <= prevSequence {
		// fmt.Printf("readBlockAt: no new block at %d\n", addr)
		return false, nil
	}
	blockContent := tmpBlock[4*8 : 4*8+blockLen]
	if blockHash != calcBlockHash(blockSequence, blockContent) {
		fmt.Printf("readBlockAt: corrupted block at %d\n", addr)
		return false, nil
	}

	prevContent = append(prevContent[0:0], blockContent...)
	prevSequence = blockSequence
	// fmt.Printf("readBlockAt: found block at %d, seq=%d\n", addr, blockSequence)
	return true, nil
}

func loadData() (content []byte, err error) {
	if storeFile == nil {
		f, err := os.OpenFile(storeFileName, os.O_RDWR, 0)
		if err != nil {
			return nil, err
		}
		storeFile = f
	}

	eraseblock := -1
	for i := 0; i < 2; i++ {
		found, err := readBlockAt(int64(eraseblockSize * i))
		if err != nil {
			return nil, err
		}
		if found {
			eraseblock = i
		}
	}
	if eraseblock == -1 {
		return nil, errNoData
	}
	off := eraseblockSize * eraseblock

	low := 1
	high := blocksPerEraseblock
	for low != high {
		mid := (low + high) / 2
		found, err := readBlockAt(int64(off + storeBlockSize*mid))
		if err != nil {
			return nil, err
		}
		if found {
			low = mid + 1
		} else {
			high = mid
		}
	}
	prevBlock = low - 1
	prevEraseblock = eraseblock

	return prevContent, nil
}

func storeData(content []byte) error {
	if len(content) > storeContentSize {
		return errTooBig
	}

	if bytes.Equal(content, prevContent) {
		return nil
	}

	prevSequence += 1

	prevBlock += 1
	if prevBlock >= blocksPerEraseblock {
		prevBlock = 0
		prevEraseblock = 1 - prevEraseblock
		if err := ioctlBlkDiscard(storeFile.Fd(), uint64(prevEraseblock*eraseblockSize), eraseblockSize); err != nil {
			return err
		}
	}

	for i := range tmpBlock {
		tmpBlock[i] = 0
	}

	binCoder.PutUint64(tmpBlock[0*8:], storeMagic)
	binCoder.PutUint64(tmpBlock[1*8:], uint64(len(content)))
	binCoder.PutUint64(tmpBlock[2*8:], calcBlockHash(prevSequence, content))
	binCoder.PutUint64(tmpBlock[3*8:], prevSequence)
	copy(tmpBlock[4*8:], content)

	addr := prevEraseblock*eraseblockSize + prevBlock*storeBlockSize
	// fmt.Printf("storeData seq=%d, addr=%d: %s\n", prevSequence, addr, string(content))
	if _, err := storeFile.WriteAt(tmpBlock, int64(addr)); err != nil {
		return err
	}
	if err := storeFile.Sync(); err != nil {
		return err
	}

	prevContent = append(prevContent[0:0], content...)
	return nil
}
