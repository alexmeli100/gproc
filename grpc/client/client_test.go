package client

import (
	"bufio"
	"fmt"
	"github.com/pkg/errors"
	"io"
	"testing"
)

func TestStreamReader(t *testing.T) {
	ch := make(chan byteBuf)
	sr := newStreamReader(ch)
	scanner := bufio.NewScanner(sr)

	el := []string{
		"my name",
		"is alex",
		"where are you now?",
		"hey I'm",
		"here",
	}

	go func() {
		lines := []string{
			"my name\nis alex\n",
			"where are you now?\n",
			"hey I'm\nhere",
		}

		for _, l := range lines {
			ch <- byteBuf{buf: []byte(l)}
		}

		ch <- byteBuf{err: io.EOF}
	}()

	var lines []string

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if scanner.Err() != nil {
		t.Fatalf("Expected nil error, got %v", scanner.Err())
	}

	if len(lines) != len(el) {
		t.Fatalf("Expected %d lines, got %d", len(el), len(lines))
	}

	for i, l := range lines {
		if l != el[i] {
			t.Errorf("Expected \"%s\" line, got \"%s\"", el[i], l)
		}
	}
}

func TestStreamReaderErr(t *testing.T) {
	ch := make(chan byteBuf)
	sr := newStreamReader(ch)
	scanner := bufio.NewScanner(sr)
	err := errors.New("random error")

	go func() {
		lines := []string{
			"my name\nis alex\n",
			"where are you now?\n",
			"hey I'm\nhere",
			"I'm here",
		}

		for i, l := range lines {

			if i == 1 {
				ch <- byteBuf{err: err}
			}

			ch <- byteBuf{buf: []byte(l)}
		}
	}()

	var lines []string

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if scanner.Err() == nil {
		t.Fatalf("Expected error \"%v\", got nil", err)
	}

	for _, l := range lines {
		fmt.Println(l)
	}

	if scanner.Err() != err {
		t.Fatalf("Expected error \"%v\", got %v", err, scanner.Err())
	}
}
