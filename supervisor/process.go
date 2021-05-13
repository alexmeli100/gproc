package supervisor

import (
	"github.com/pkg/errors"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultStreamChanSize = 16384
)

var (
	ErrFailedStopProc   = errors.New("failed to stop process, already in Initial/Final state")
	ErrFailedSignalProc = errors.New("failed to signal process, already in Initial/Final state")
)

type ProcessState string

const (
	Running  ProcessState = "running"
	Finished ProcessState = "finished"
	Fatal    ProcessState = "failed"
)

// Process is a wrapper around exec.Cmd to run linux processes
type Process struct {
	access       *sync.RWMutex
	cmd          *exec.Cmd
	streamStdOut chan byte
	streamStdErr chan byte
	stdOut       *StreamOutput
	stdErr       *StreamOutput
	status       Status
	done         chan struct{}
}

// Status is the status of a process
type Status struct {
	Cmd        string
	PID        int
	State      ProcessState
	Error      error
	ExitCode   int
	StartTime  time.Time
	StopTime   time.Time
	StopByUser bool
}

// NewProcess creates and returns a new process without running it
func NewProcess(name string, args []string) *Process {
	a := strings.Join(args, " ")

	status := Status{
		Cmd:      name + " " + a,
		ExitCode: -1,
	}

	cmd := exec.Command(name, args...)

	stdOutChan := make(chan byte, DefaultStreamChanSize)
	stdErrChan := make(chan byte, DefaultStreamChanSize)
	stdOut := NewStreamOutput(stdOutChan)
	stdErr := NewStreamOutput(stdErrChan)
	cmd.Stdout = stdOut
	cmd.Stderr = stdErr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	process := &Process{
		cmd:          cmd,
		status:       status,
		done:         make(chan struct{}),
		access:       &sync.RWMutex{},
		streamStdOut: stdOutChan,
		streamStdErr: stdErrChan,
		stdOut:       stdOut,
		stdErr:       stdErr,
	}

	return process
}

// Wait waits for the process to finish running and returns the final status
func (p *Process) Wait() Status {
	<-p.Done()

	return p.GetStatus()
}

// Start starts the process and returns a channel the caller can
// use to retrieve the final status
func (p *Process) Start() {
	p.access.Lock()
	defer p.access.Unlock()

	p.status.StartTime = time.Now()
	if err := p.cmd.Start(); err != nil {
		// process failed to start.
		// exit immediately
		p.status.StopTime = time.Now()
		p.status.Error = err
		p.status.State = Fatal
		close(p.done)

		return
	}

	p.status.PID = p.cmd.Process.Pid
	p.status.State = Running
	go p.wait()
}

// wait waits for a process to finish running and sets it's final status
func (p *Process) wait() {
	// send the final status and close done channel to signal to
	// goroutines the process has finished running
	defer func() {
		close(p.done)
	}()

	err := p.cmd.Wait()
	now := time.Now()
	exitCode := 0

	p.access.Lock()
	defer p.access.Unlock()

	// get the exit code of the process
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			err = nil
			exitCode = exitErr.ExitCode()

			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				if status.Signaled() {
					err = errors.New(exitErr.Error())
				}
			}
		}
	}

	// set final status of the process
	p.status.Error = err
	p.status.ExitCode = exitCode
	p.status.StopTime = now
	p.status.State = Finished
}

// GetStatus returns the current status of the process
func (p *Process) GetStatus() Status {
	p.access.RLock()
	defer p.access.RUnlock()

	return p.status
}

// Stop stops a process by sending it's group a SIGTERM signal
func (p *Process) Stop(byUser bool) error {
	p.access.Lock()
	defer p.access.Unlock()

	if p.status.State != Running {
		return ErrFailedStopProc
	}

	p.status.StopByUser = byUser

	return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
}

// Signal sends an os signal to the process
func (p *Process) Signal(sig syscall.Signal) error {
	p.access.Lock()
	defer p.access.Unlock()

	if p.status.State != Running {
		return ErrFailedSignalProc
	}

	return syscall.Kill(-p.cmd.Process.Pid, sig)
}

// StdOut returns the standard output streaming channel
func (p *Process) StdOut() <-chan byte {
	return p.streamStdOut
}

// StdErr returns the standard error streaming channel
func (p *Process) StdErr() <-chan byte {
	return p.streamStdErr
}

// Done returnes a channel that is closed when the process finishes running
func (p *Process) Done() chan struct{} {
	return p.done
}

// StreamOutput streams bytes of output through the provided channel
type StreamOutput struct {
	streamChan chan byte
}

// NewStreamOutput returns an instance of a StreamOuput using the provided channel
func NewStreamOutput(streamChan chan byte) *StreamOutput {
	s := &StreamOutput{
		streamChan: streamChan,
	}

	return s
}

// TODO: stream bytes instead of lines
// Write implements the io.Writer interface for StreamOutput
func (s *StreamOutput) Write(buf []byte) (int, error) {
	n := len(buf)

	for _, b := range buf {
		s.streamChan <- b
	}

	return n, nil
}

// Bytes returnes the streaming channel.
//It's the same channel passed when creating a StreamOutput
func (s *StreamOutput) Bytes() chan byte {
	return s.streamChan
}
