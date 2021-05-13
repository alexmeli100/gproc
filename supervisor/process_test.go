package supervisor

import (
	"testing"
	"time"
)

func TestProcessBasicStartStop(t *testing.T) {
	p := NewProcess("ping", []string{"localhost"})

	p.Start()

	time.Sleep(5 * time.Second)

	err := p.Stop(true)

	if err != nil {
		t.Errorf("Expected nil error. Got %s", err.Error())
	}

	st := p.Wait()

	if st.State != Finished {
		t.Errorf("Expected final state \"finished\", got \"%s\"", st.State)
	}
}

func TestProcessOk(t *testing.T) {
	p := NewProcess("echo", []string{"line1"})
	now := time.Now().Unix()

	p.Start()

	s := p.Wait()

	if s.StartTime.Unix() < now {
		t.Errorf("Start time %d", s.StartTime.Unix())
	}

	var line []byte

	for i := 0; i < 5; i++ {
		line = append(line, <-p.StdOut())
	}

	if string(line) != "line1" {
		t.Errorf("Expected output \"foo\". Got \"%s\"", line)
	}

	if s.ExitCode != 0 {
		t.Errorf("Expected exit code 0, Got %d", s.ExitCode)
	}

	if s.Error != nil {
		t.Errorf("Expected nil error, Got %s", s.Error.Error())
	}
}
