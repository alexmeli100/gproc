package supervisor

import (
	"bytes"
	"fmt"
	"github.com/pkg/errors"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultLineBufferSize = 16384
	DefaultStreamChanSize = 200
)

type ProcessState uint8

const (
	Initial ProcessState = iota
	Starting
	Running
	Stopping
	Finished
	Interrupted
	Fatal
)

func (s ProcessState) String() string {
	switch s {
	case Initial:
		return "initial"
	case Starting:
		return "starting"
	case Running:
		return "running"
	case Stopping:
		return "stopping"
	case Finished:
		return "finished"
	case Fatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// Process is a wrapper around exec.Cmd to run linux processes
type Process struct {
	*sync.Mutex
	cmd          *exec.Cmd
	streamStdOut chan string
	streamStdErr chan string
	stdOut       *StreamOutput
	stdErr       *StreamOutput
	state        ProcessState
	status       Status
	statusChan   chan *Status
	done         chan struct{}
	stateLock    *sync.Mutex
}

// Status is the status of a process
type Status struct {
	Cmd        string
	PID        int
	State      ProcessState
	Error      error
	ExitCode   int
	StartTime  int64
	StopTime   int64
	StopByUser bool
}

// NewProcess creates and returns a new process without running it
func NewProcess(name string, args []string) *Process {
	a := strings.Join(args, " ")

	status := Status{
		Cmd:        name + " " + a,
		PID:        0,
		Error:      nil,
		ExitCode:   -1,
		StopByUser: false,
	}

	cmd := exec.Command(name, args...)

	stdOutChan := make(chan string, DefaultStreamChanSize)
	stdErrChan := make(chan string, DefaultStreamChanSize)
	stdOut := NewStreamOutput(stdOutChan)
	stdErr := NewStreamOutput(stdErrChan)

	process := &Process{
		cmd:          cmd,
		status:       status,
		done:         make(chan struct{}),
		Mutex:        &sync.Mutex{},
		stateLock:    &sync.Mutex{},
		streamStdOut: stdOutChan,
		streamStdErr: stdErrChan,
		stdOut:       stdOut,
		stdErr:       stdErr,
	}

	return process

}

// setState sets the state of a process
func (p *Process) setState(state ProcessState) {
	// cannot the set the state of a finished process
	if p.state == Finished || p.state == state {
		return
	}

	p.stateLock.Lock()
	defer p.stateLock.Unlock()

	// the next state can only be Starting if the state is Initial
	if p.state == Initial {
		p.state = Starting
	} else {
		p.state = state
	}

	p.status.State = p.state
}

// IsInitialState is true if process state is Initial
func (p *Process) IsInitialState() bool {
	p.stateLock.Lock()
	defer p.stateLock.Unlock()

	return p.state == Initial
}

// IsRunningState is true if process is Starting or Running
func (p *Process) IsRunningState() bool {
	p.stateLock.Lock()
	defer p.stateLock.Unlock()

	return p.state == Running || p.state == Starting
}

// IsFinalState is true if process is Finished or Interrupted
func (p *Process) IsFinalState() bool {
	p.stateLock.Lock()
	defer p.stateLock.Unlock()

	return p.state == Finished || p.state == Interrupted
}

// Start starts the process and returns a channel the caller can
// use to retrieve the final status
func (p *Process) Start() <-chan *Status {
	p.Lock()
	defer p.Unlock()

	if p.statusChan != nil {
		return p.statusChan
	}

	p.statusChan = make(chan *Status, 1)
	p.setState(Starting)

	p.cmd.Stdout = p.stdOut
	p.cmd.Stderr = p.stdErr

	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	now := time.Now()
	if err := p.cmd.Start(); err != nil {
		// process failed to start.
		// exit immediately
		p.status.StartTime = now.UnixNano()
		p.status.StopTime = time.Now().UnixNano()
		p.status.Error = err
		p.setState(Fatal)
		p.statusChan <- &p.status
		close(p.done)

		return p.statusChan
	}

	p.status.StartTime = now.UnixNano()
	p.status.PID = p.cmd.Process.Pid
	p.setState(Running)
	go p.wait()

	return p.statusChan
}

// wait waits for a process to finish running and sets it's final status
func (p *Process) wait() {
	// send the final status and close done channel to signal to
	// goroutines the process has finished running
	defer func() {
		p.statusChan <- p.GetStatus()
		close(p.done)
	}()

	err := p.cmd.Wait()
	now := time.Now()
	exitCode := 0

	p.Lock()
	defer p.Unlock()

	// get the exit code of the process
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			err = nil

			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()

				if status.Signaled() {
					err = errors.New(exitErr.Error())
					p.setState(Interrupted)
				}

			}
		}
	}

	// set final status of the process
	p.status.Error = err
	p.status.ExitCode = exitCode
	p.status.StopTime = now.UnixNano()
	p.setState(Finished)
}

// GetStatus returns the current status of the process
func (p *Process) GetStatus() *Status {
	p.Lock()
	defer p.Unlock()

	return &p.status
}

// Stop stops a process by sending it's group a SIGTERM signal
func (p *Process) Stop(byUser bool) error {
	p.Lock()
	defer p.Unlock()

	if p.IsInitialState() || p.IsFinalState() {
		return nil
	}

	p.setState(Stopping)
	p.status.StopByUser = byUser

	return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
}

// Signal sends an os signal to the process
func (p *Process) Signal(sig syscall.Signal) error {
	p.Lock()
	defer p.Unlock()

	if p.IsInitialState() || p.IsFinalState() {
		return nil
	}

	if sig == syscall.SIGTERM {
		p.setState(Stopping)
	}

	return syscall.Kill(-p.cmd.Process.Pid, sig)
}

// StdOut returns the standard output streaming channel
func (p *Process) StdOut() chan string {
	return p.streamStdOut
}

// StdErr returns the standard error streaming channel
func (p *Process) StdErr() chan string {
	return p.streamStdErr
}

// Done returnes a channel that is closed when the process finishes running
func (p *Process) Done() chan struct{} {
	return p.done
}

type StreamOutputOptions func(*StreamOutput)

// ErrLineBufferOverflow is an error type returned when the buffer in StreamOutput used to
// buffer lines is too small. Try increasing the buffer size if you get this error
type ErrLineBufferOverflow struct {
	line       string
	bufferSize int
	freeBytes  int
}

func NewErrLineBufferOverflow(line string, bufsize, freeBytes int) ErrLineBufferOverflow {
	return ErrLineBufferOverflow{
		line:       line,
		bufferSize: bufsize,
		freeBytes:  freeBytes,
	}
}

func (e ErrLineBufferOverflow) Error() string {
	err := fmt.Sprintf(
		`line does not contain newline and is %d 
				bytes too long to buffer (buffer size: %d).
				Try increasing the buffer size to avoid this error`,
		len(e.line)-e.bufferSize, e.freeBytes)

	return err
}

// StreamOutput streams lines of output through the provided channel
type StreamOutput struct {
	streamChan chan string
	bufSize    int
	buf        []byte
	lastChar   int
}

// SetLineBufferSize sets the buffer size of a StreamOutput
func SetLineBufferSize(size int) StreamOutputOptions {
	return func(o *StreamOutput) {
		o.bufSize = size
		o.buf = make([]byte, size)
	}
}

// NewStreamOutput returns an instance of a StreamOuput using the provided channel
func NewStreamOutput(streamChan chan string, opts ...StreamOutputOptions) *StreamOutput {
	s := &StreamOutput{
		streamChan: streamChan,
		bufSize:    DefaultLineBufferSize,
		buf:        make([]byte, DefaultLineBufferSize),
		lastChar:   0,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Write implements the io.Writer interface for StreamOutput
func (s *StreamOutput) Write(b []byte) (int, error) {
	n := len(b)
	firstChar := 0

	for {
		// find the next line in the buffer
		newLineOffset := bytes.IndexByte(b[firstChar:], '\n')

		if newLineOffset < 0 {
			break // no newlines in stream
		}

		lastCharOffset := firstChar + newLineOffset

		// check for carriage return and strip it if present
		if newLineOffset > 0 && b[lastCharOffset-1] == '\r' {
			lastCharOffset -= 1
		}

		var line string

		// preprend any lines if buffer is not empty
		if s.lastChar > 0 {
			line = string(s.buf[0:s.lastChar])
			s.lastChar = 0
		}

		line += string(b[firstChar:lastCharOffset])

		s.streamChan <- line
		firstChar += newLineOffset + 1
	}

	if firstChar < n {
		remainingBytes := len(b[firstChar:])
		free := len(s.buf[s.lastChar:])

		if remainingBytes > free {
			var line string

			if s.lastChar > 0 {
				line = string(s.buf[0:s.lastChar])
			}

			line += string(b[firstChar:])

			return firstChar, NewErrLineBufferOverflow(line, s.bufSize, free)
		}

		copy(s.buf[s.lastChar:], b[firstChar:])
		s.lastChar += remainingBytes
	}

	return n, nil
}

// Lines returnes the streaming channel.
//It's the same channel passed when creating a StreamOutput
func (s *StreamOutput) Lines() chan string {
	return s.streamChan
}
