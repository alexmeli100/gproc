package supervisor

import (
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSupervisorSimpleStartStop(t *testing.T) {
	logger := zap.NewNop()

	s := NewSupervisor(logger)

	st, err := s.Start("ping", []string{"localhost"})

	if err != nil {
		t.Fatalf("expected nil error, Got %s", err.Error())
	}

	if !s.HasJob(st.Id) {
		t.Fatalf("supervisor should have job with id %s", st.Id)
	}

	time.Sleep(5 * time.Second)

	st, err = s.Stop(st.Id)

	if err != nil {
		t.Fatalf("stop failed with error %s", err.Error())
	}

	if st.Status.State != Finished {
		t.Errorf("Expected \"Finished\" for job. Got \"%d\"", st.Status.State)
	}

}

func TestInvalidProcess(t *testing.T) {
	logger := zap.NewNop()

	s := NewSupervisor(logger)
	_, err := s.Start("hiosfs", []string{""})

	if !errors.Is(err, ErrExecNotFound) {
		t.Errorf("Expected \"ErrExecNotFound\". Got \"%s\"", err.Error())
	}

	// test with invalid argument to valid command
	// the ls command should run run and return a positive exit code
	st, err := s.Start("ls", []string{"/some/random/path"})

	if err != nil {
		t.Fatalf("Expected nil error, Got %s", err.Error())
	}

	wait, err := s.Wait(st.Id)

	if err != nil {
		t.Fatalf("Expected nil error, Got %s", err.Error())
	}
	<-wait

	st, err = s.Status(st.Id)

	if err != nil {
		t.Fatalf("Expected nil error, Got %s", err.Error())
	}

	if st.Status.Error != nil {
		t.Errorf("Expected nil error, Got %s", st.Status.Error.Error())
	}

	if st.Status.ExitCode <= 0 {
		t.Errorf("Expected positive exit code, Got %d", st.Status.ExitCode)
	}

	if st.Status.State != Finished {
		t.Errorf("Expected state \"finished\", got \"%s\"", st.Status.State.String())
	}

}

func TestGetOutput(t *testing.T) {
	logger := zap.NewNop()

	s := NewSupervisor(logger)

	st, err := s.Start("echo", []string{"random message"})

	if err != nil {
		t.Fatalf("expected nil error, Got %s", err.Error())
	}

	wait, err := s.Wait(st.Id)

	if err != nil {
		t.Fatalf("expected nil error, Got %s", err.Error())
	}

	<-wait
	time.Sleep(5 * time.Second)

	o, err := s.GetOutput(st.Id)

	if err != nil {
		t.Fatalf("expected nil error, Got %s", err.Error())
	}

	if len(o) != 1 {
		t.Fatalf("Expected output slice of length 1. Got length %d", len(o))
	}

	if o[0].Msg != "random message" {
		t.Errorf("Expected output message \"random message\". Got \"%s\"", o[0].Msg)
	}
}

func TestOutputStreaming(t *testing.T) {
	logger := zap.NewNop()

	s := NewSupervisor(logger)

	ch := make(chan LogOutput)

	st, err := s.Start("ping", []string{"localhost"})

	if err != nil {
		t.Fatalf("expected nil error, Got %s", err.Error())
	}

	time.Sleep(5 * time.Second)
	var chLines []LogOutput
	lines, err := s.WatchOutput(st.Id, ch)

	m := sync.Mutex{}
	go func() {
		for l := range ch {
			m.Lock()
			chLines = append(chLines, l)
			m.Unlock()
		}
	}()

	time.Sleep(5 * time.Second)
	st, err = s.Stop(st.Id)

	if err != nil {
		t.Fatalf("stop failed with error %s", err.Error())
	}

	m.Lock()
	lines = append(lines, chLines...)
	m.Unlock()

	var seq []int

	pat := regexp.MustCompile(`icmp_seq=(\d+)`)

	if len(lines) < 2 {
		t.Fatalf("Expected output from process")
	}

	for _, l := range lines[1:] {
		match := pat.FindStringSubmatch(l.Msg)
		a, err := strconv.Atoi(match[1])

		if err != nil {
			t.Fatalf("Expected number in pattern. Got \"%s\"", match[1])
		}
		seq = append(seq, a)
	}

	for i := 0; i < len(seq)-1; i++ {
		if seq[i+1]-seq[i] != 1 {
			t.Errorf("Expected a difference of 1 in icmp seq. Got \"%d\"", seq[i+1]-seq[i])
		}
	}
}

func TestSupervisorStopAll(t *testing.T) {
	// add 3 ping processess
	logger := zap.NewNop()
	var ids []string
	s := NewSupervisor(logger)

	for i := 0; i < 3; i++ {
		st, err := s.Start("ping", []string{"localhost"})

		if err != nil {
			t.Fatalf("expected nil error, Got %s", err.Error())
		}

		ids = append(ids, st.Id)
	}

	// run processes for 5 seconds
	time.Sleep(5 * time.Second)

	s.StopAll()
	s.WaitAll()

	for _, id := range ids {
		st, err := s.Status(id)

		if err != nil {
			t.Fatalf("expected nil error, Got %s", err.Error())
		}

		if st.Status.State != Finished {
			t.Errorf("Expected final state \"finished\", Got \"%s\"", st.Status.State.String())
		}
	}

}

func TestSupervisorStopDeadProc(t *testing.T) {
	logger := zap.NewNop()

	s := NewSupervisor(logger)

	st, err := s.Start("echo", []string{"random message"})

	if err != nil {
		t.Fatalf("expected nil error, Got %s", err.Error())
	}

	wait, err := s.Wait(st.Id)

	if err != nil {
		t.Fatal(err)
	}

	<-wait

	st, err = s.Stop(st.Id)

	if err != nil {
		t.Fatal(err)
	}

	if st.Status.State != Finished {
		t.Errorf("Expected \"Finished\" state. Got \"%s\"", st.Status.State.String())
	}

	st, err = s.Start("ping", []string{"localhost"})

	if err != nil {
		t.Fatalf("expected nil error, Got %s", err.Error())
	}

	wait, err = s.Wait(st.Id)

	if err != nil {
		t.Fatal(err)
	}

	// wait for the process to run a bit
	time.Sleep(5 * time.Second)
	st, err = s.Stop(st.Id)

	if err != nil {
		t.Fatalf("stop failed with error %s", err.Error())
	}

	// stop the job again
	st, err = s.Stop(st.Id)

	if err != nil {
		t.Fatalf("stop failed with error %s", err.Error())
	}

	if st.Status.State != Finished {
		t.Errorf("Expected \"Finished\" state. Got \"%s\"", st.Status.State.String())
	}
}
