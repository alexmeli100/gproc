package supervisor

import (
	"testing"
	"time"
)

func TestProcessBasicStartStop(t *testing.T) {
	p := NewProcess("ping", []string{"localhost"})

	_ = p.Start()

	time.Sleep(5 * time.Second)

	err := p.Stop(true)

	if err != nil {
		t.Errorf("Expected nil error. Got %s", err.Error())
	}

	<-p.Done()

	st := p.GetStatus()

	if st.State != Finished {
		t.Errorf("Expected final state \"finished\", got \"%s\"", st.State.String())
	}
}

func TestProcessOk(t *testing.T) {
	p := NewProcess("echo", []string{"line1"})
	now := time.Now().Unix()

	if p.state != Initial {
		t.Errorf("got state %d, expected Initial", p.state)
	}

	s := <-p.Start()
	line := <-p.StdOut()

	if s.StartTime < now {
		t.Errorf("Start time %d", s.StartTime)
	}

	if line != "line1" {
		t.Errorf("Expected output \"foo\". Got \"%s\"", line)
	}

	if s.ExitCode != 0 {
		t.Errorf("Expected exit code 0, Got %d", s.ExitCode)
	}

	if s.Error != nil {
		t.Errorf("Expected nil error, Got %s", s.Error.Error())
	}
}

func TestBasicStreamBuffer(t *testing.T) {
	ch := make(chan string, 5)
	o := NewStreamOutput(ch)

	tcs := []struct {
		line     string
		expected []string
	}{
		{"line1\nline2\nline3\n", []string{"line1", "line2", "line3"}},
		{"line1\r\nline2\r\nline3\r\n", []string{"line1", "line2", "line3"}},
		{"\n\n\n", []string{"", "", ""}},
	}

	for i, tc := range tcs {
		n, err := o.Write([]byte(tc.line))

		if n != len(tc.line) {
			t.Fatalf("expected %d bytes written, got %d", len(tc.line), n)
		}

		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		var got []string

	lines:
		for {
			select {
			case l := <-ch:
				got = append(got, l)
			default:
				break lines
			}
		}

		if len(got) != len(tc.expected) {
			t.Fatalf("expected %d lines, got %d for tc %d", len(tc.expected), len(got), i)
		}

		for i, l := range tc.expected {
			if got[i] != l {
				t.Errorf("expected line, \"%s\", got \"%s\"", got[i], l)
			}
		}
	}
}
