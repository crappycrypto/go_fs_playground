package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"testing"
)

func IntMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestIntMinBasic(t *testing.T) {
	fsFile := "test.ext4"
	targetFile := "ext4_simple.h"
	testDir := "gen_ext4_defs/"
	tmpFile := "tmp.h"

	cmd := exec.Command("make_ext4fs", "-l", "10M", fsFile, testDir)
	_ = cmd.Run()

	fsFileHandle, _ := os.Open(fsFile)
	tmpFileHandle, _ := os.Create(tmpFile)
	readFile(fsFileHandle, targetFile, tmpFileHandle)
	_ = fsFileHandle.Close()
	_ = tmpFileHandle.Close()

	f1, _ := os.Open(testDir + targetFile)
	f2, _ := os.Open(tmpFile)
	stat1, _ := f1.Stat()
	stat2, _ := f2.Stat()
	if stat1.Size() != stat2.Size() {
		t.Errorf("Sizes differ")
	}

	buf1 := make([]byte, 4096)
	buf2 := make([]byte, 4096)
	eof := false
	for eof {
		n1, err1 := f1.Read(buf1)
		switch err1 {
		case io.EOF:
			eof = true
		case nil:
		default:
			t.Errorf("Error while reading")
		}
		n2, err2 := f2.Read(buf2)
		switch err2 {
		case io.EOF:
			eof = true
		case nil:
		default:
			t.Errorf("Error while reading")
		}
		if n1 != n2 {
			t.Errorf("Read sizes differ")
		}
		if !bytes.Equal(buf1[:n1], buf2[:n2]) {
			off1 , _ := f1.Seek(0, io.SeekCurrent)
			off2 , _ := f2.Seek(0, io.SeekCurrent)
			t.Errorf("Buffers differ at offset %d %d", off1, off2)
			break
		}
	}

}
