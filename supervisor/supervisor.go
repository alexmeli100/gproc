package supervisor

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"os/exec"
	"sync"
)

var (
	ErrJobNotFound  = errors.New("invalid job id")
	ErrExecNotFound = errors.New("executable not found on system")
)

const (
	StdOut = iota
	StdErr
)

// LogOutput is a line of output from a running job either
// from standard output or stadard error
type LogOutput struct {
	Type uint8
	Msg  string
}

// Supervisor manages all processes
type Supervisor struct {
	access         *sync.RWMutex
	jobs           *sync.Map
	logger         *zap.Logger
	ouputBuffers   map[string][]LogOutput
	outputChannels map[string]*logWatchers
	wg             *sync.WaitGroup
}

// NewSupervisor returns an instance of a Supervisor
func NewSupervisor(logger *zap.Logger) *Supervisor {
	return &Supervisor{
		access:         &sync.RWMutex{},
		jobs:           &sync.Map{},
		logger:         logger,
		ouputBuffers:   make(map[string][]LogOutput),
		outputChannels: make(map[string]*logWatchers),
		wg:             &sync.WaitGroup{},
	}
}

// JobStatus is the status of a job with it's id
type JobStatus struct {
	Id     string
	Status *Status
}

// logWatchers is a wrapper to manage output streaming for each job.
// It contains a set of chan LogOutput for each job.
// WatchOutput adds the provided channel to this set so the caller can subscribe
// to lines of output from the job
type logWatchers struct {
	*sync.RWMutex
	watchers map[chan LogOutput]bool
}

// newLogWatcher returns an instance of a logWatchers
func newLogWatcher() *logWatchers {
	return &logWatchers{
		RWMutex:  &sync.RWMutex{},
		watchers: make(map[chan LogOutput]bool),
	}
}

// addWatcher adds the provided channel to the logWatchers set
func (l *logWatchers) addWatcher(c chan LogOutput) {
	l.Lock()
	defer l.Unlock()

	l.watchers[c] = true
}

// sendLog sends the log message to all the subscribers in the logWatchers set
func (l *logWatchers) sendLog(log LogOutput) {
	l.RLock()
	defer l.RUnlock()

	for c := range l.watchers {
		c <- log
	}
}

// removeLogWatcher removes the channel from the logWatchers set
func (l *logWatchers) removeLogWatcher(c chan LogOutput) {
	l.Lock()
	defer l.Unlock()

	delete(l.watchers, c)
}

// Start starts a job and returns the current status. An ErrExecNotFound
// is returned the executable cannot be found by the system.
// A unique id is generated for the job and returned in JobStatus
func (s *Supervisor) Start(cmd string, args []string) (*JobStatus, error) {
	if cmd == "" {
		msg := "cannot add job, no executable defined"
		s.logger.Error(msg)
		return nil, errors.New(msg)
	}

	_, err := exec.LookPath(cmd)

	if err != nil {
		msg := fmt.Sprintf("cannot add job, the system cannot find the given executable \"%s\"", cmd)
		s.logger.Error(msg)
		return nil, ErrExecNotFound
	}

	jobId := uuid.New().String()
	p := NewProcess(cmd, args)
	sc := p.Start()

	s.wg.Add(1)
	go func(id string, p *Process, c <-chan *Status) {
		defer s.wg.Done()
		s.run(id, p, sc)
	}(jobId, p, sc)

	s.logger.Info("Added Job: ",
		zap.String("id", jobId),
		zap.String("name", cmd),
		zap.Strings("args", args))

	s.access.Lock()
	s.jobs.Store(jobId, p)
	s.outputChannels[jobId] = newLogWatcher()
	s.ouputBuffers[jobId] = []LogOutput{}
	s.access.Unlock()

	status := p.GetStatus()

	return &JobStatus{jobId, status}, nil
}

// run listens for output from the running job through it's StdOut and StdErr channels.
// Each line of output is first added to the job's line buffer before it's sent to the log watchers.
func (s *Supervisor) run(id string, proc *Process, sc <-chan *Status) {
	go func(id string, p *Process) {
		for {
			select {
			case l := <-p.StdOut():
				line := LogOutput{StdOut, l}

				s.access.Lock()
				s.ouputBuffers[id] = append(s.ouputBuffers[id], line)
				watchers := s.outputChannels[id]
				s.access.Unlock()

				watchers.sendLog(line)
			case l := <-p.StdErr():
				line := LogOutput{StdErr, l}

				s.access.Lock()
				s.ouputBuffers[id] = append(s.ouputBuffers[id], line)
				watchers := s.outputChannels[id]
				s.access.Unlock()

				watchers.sendLog(line)
			case <-p.Done():
				return
			}

		}
	}(id, proc)

	fs := <-sc

	if fs.ExitCode != 0 || fs.Error != nil {
		// process didn't exit successfully. Log the error
		s.logger.Error("Process didn't exit successfully",
			zap.String("id", id),
			zap.Int("exit code", fs.ExitCode),
			zap.Error(fs.Error))
	}
}

// Wait returns the done channel from Process. This channel is closed when the process stops running
func (s *Supervisor) Wait(id string) (chan struct{}, error) {
	s.access.Lock()
	defer s.access.Unlock()

	p, ok := s.jobs.Load(id)
	job := p.(*Process)
	if !ok {
		s.logger.Error("Called wait on a job that does not exist", zap.String("id", id))
		return nil, ErrJobNotFound
	}

	return job.Done(), nil
}

// WaitAll waits for all the processes to finish running
func (s *Supervisor) WaitAll() {
	s.wg.Wait()
}

// Stop stops a job by sending it's process group a SIGTERM signal
func (s *Supervisor) Stop(id string) (*JobStatus, error) {
	s.access.Lock()
	defer s.access.Unlock()

	p, ok := s.jobs.Load(id)
	job := p.(*Process)

	if !ok {
		s.logger.Error("Cannot find job with the given id", zap.String("id", id))
		return nil, ErrJobNotFound
	}

	if err := job.Stop(true); err != nil {
		s.logger.Error("Cannot stop process", zap.String("id", id))
		return nil, errors.Wrap(err, "cannot stop process")
	}

	// Wait for process to exit completely
	<-job.Done()

	// return final status to caller
	st := job.GetStatus()
	return &JobStatus{id, st}, nil
}

// StopAll stops a the jobs in the system
func (s *Supervisor) StopAll() {
	s.jobs.Range(func(id, j interface{}) bool {
		job := j.(*Process)

		if err := job.Stop(false); err != nil {
			s.logger.Error("Cannot stop process", zap.String("id", id.(string)))
		}

		return true
	})
}

// Status returns the current status of a job
func (s *Supervisor) Status(id string) (*JobStatus, error) {
	p, ok := s.jobs.Load(id)
	job := p.(*Process)

	if !ok {
		s.logger.Error("Cannot find job with the given id", zap.String("id", id))
		return nil, ErrJobNotFound
	}

	st := job.GetStatus()

	return &JobStatus{id, st}, nil
}

// GetOutput returns the line buffer for the job
func (s *Supervisor) GetOutput(id string) ([]LogOutput, error) {
	s.access.Lock()
	s.access.Unlock()

	lines, ok := s.ouputBuffers[id]

	if !ok {
		s.logger.Error("Cannot find job with the given id", zap.String("id", id))
		return nil, ErrJobNotFound
	}

	return lines, nil
}

// HasJob checks if the job with the given id has been started by the Supervisor
func (s *Supervisor) HasJob(id string) bool {
	_, ok := s.jobs.Load(id)

	return ok
}

// WatchOutput subscribes the caller to the jobs output through the provided channel.
// It returns all previous lines of output to the caller
func (s *Supervisor) WatchOutput(id string, c chan LogOutput) ([]LogOutput, error) {
	s.access.Lock()
	defer s.access.Unlock()

	if !s.HasJob(id) {
		s.logger.Error("cannot find job with the given id", zap.String("id", id))
		return nil, ErrJobNotFound
	}

	m := s.outputChannels[id]

	m.addWatcher(c)
	lines := s.ouputBuffers[id]

	return lines, nil
}

// UnWatchOutput unsubscribes the caller from the jobs channel.
// The provided channel must be the one passed to WatchOutput
func (s *Supervisor) UnWatchOutput(id string, c chan LogOutput) {
	s.access.Lock()
	defer s.access.Unlock()

	m, ok := s.outputChannels[id]

	if !ok {
		return
	}

	m.removeLogWatcher(c)
}
