package supervisor

import (
	"github.com/pkg/errors"
	"go.uber.org/zap"
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

	wait, err := s.Wait(st.Id)

	if err != nil {
		t.Fatal(err)
	}

	<-wait
	st, err = s.Status(st.Id)

	if err != nil {
		t.Fatalf("expected nil error, Got %s", err.Error())
	}

	if st.Status.State != Finished {
		t.Errorf("Expected \"Finished\" for job. Got \"%s\"", st.Status.State)
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
	// the ls command should run and return a positive exit code
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
		t.Errorf("Expected state \"finished\", got \"%s\"", st.Status.State)
	}

}

func TestGetOutput(t *testing.T) {
	logger := zap.NewNop()

	s := NewSupervisor(logger)

	msg := "random message"
	st, err := s.Start("echo", []string{msg})

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

	// newline added by echo command
	if len(msg)+1 != len(o) {
		t.Fatalf("Expected output slice of length %d. Got length %d", len(msg), len(o))
	}

	var msg2 []byte

	for _, b := range o {
		msg2 = append(msg2, b.Msg)
	}

	// newline added by echo command
	if string(msg2) != "random message\n" {
		t.Errorf("Expected output message \"random message\". Got \"%s\"", msg2)
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
			t.Errorf("Expected final state \"finished\", Got \"%s\"", st.Status.State)
		}
	}

}

// Tests that stopping a finished process doesn't do anything and simply returns the final status
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

	// Wait for the process to finish
	<-wait

	// Should return Err
	st, err = s.Stop(st.Id)

	if !errors.Is(err, ErrFailedStopProc) {
		t.Fatalf("Expected ErrFaildedStopProc, got %s", err.Error())
	}

	// test with a long running process
	st, err = s.Start("ping", []string{"localhost"})

	if err != nil {
		t.Fatalf("expected nil error, Got %s", err.Error())
	}

	// wait for the process to run a bit
	time.Sleep(5 * time.Second)

	// stop and wait for process to finish
	st, err = s.Stop(st.Id)

	if err != nil {
		t.Fatalf("stop failed with error %s", err.Error())
	}

	wait, err = s.Wait(st.Id)

	if err != nil {
		t.Fatal(err)
	}

	<-wait

	// Get final status
	st, err = s.Status(st.Id)

	if err != nil {
		t.Fatalf("expected nil error, Got %s", err.Error())
	}

	if st.Status.State != Finished {
		t.Errorf("Expected \"Finished\" state. Got \"%s\"", st.Status.State)
	}

	// stop the job again
	st, err = s.Stop(st.Id)

	if !errors.Is(err, ErrFailedStopProc) {
		t.Fatalf("Expected ErrFaildedStopProc, got %s", err.Error())
	}
}
