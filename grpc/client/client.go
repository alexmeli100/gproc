package client

import (
	"bufio"
	"context"
	pb "github.com/alexmeli100/teleport-assessment/grpc"
	"github.com/oleiade/lane"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"io"
	"sync"
)

type WorkerClient struct {
	client pb.WorkerServiceClient
}

func NewWorkerClient(ctx context.Context, addr string, cred credentials.TransportCredentials) (*WorkerClient, error) {
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(cred))

	if err != nil {
		return nil, err
	}

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	client := pb.NewWorkerServiceClient(conn)

	return &WorkerClient{client}, nil
}

func (w *WorkerClient) Start(ctx context.Context, cmd string, args []string) (*pb.JobStatus, error) {
	req := &pb.JobRequest{Cmd: cmd, Args: args}

	s, err := w.client.Start(ctx, req)

	if err != nil {
		return nil, errors.Wrap(err, "failed to start job")
	}

	return s, nil
}

func (w *WorkerClient) Stop(ctx context.Context, id string) (*pb.JobStatus, error) {
	req := &pb.StopRequest{Id: id}
	newCtx := metadata.AppendToOutgoingContext(ctx, "jobid", id)

	s, err := w.client.Stop(newCtx, req)

	if err != nil {
		return nil, errors.Wrap(err, "failed to stop job")
	}

	return s, err
}

func (w *WorkerClient) Status(ctx context.Context, id string) (*pb.JobStatus, error) {
	req := &pb.StatusRequest{Id: id}
	newCtx := metadata.AppendToOutgoingContext(ctx, "jobid", id)

	s, err := w.client.Status(newCtx, req)

	if err != nil {
		return nil, errors.Wrap(err, "failed to get job status")
	}

	return s, nil
}

type byteBuf struct {
	buf []byte
	err error
}

type Line struct {
	Msg     string
	Channel pb.OutputChannel
}

type OutputMessage struct {
	Err    error
	Output Line
}

type streamReader struct {
	ch   chan byteBuf
	bufs *lane.Queue
	err  error
}

func newStreamReader(ch chan byteBuf) *streamReader {
	return &streamReader{
		ch:   ch,
		bufs: lane.NewQueue(),
	}
}

func (s *streamReader) Read(b []byte) (int, error) {
	if s.err == io.EOF && s.bufs.Empty() {
		return 0, io.EOF
	} else if s.err != nil {
		return 0, s.err
	}

	read := 0

	for {
		select {
		case buf := <-s.ch:
			if buf.err != nil {
				s.err = buf.err
			} else {
				s.bufs.Enqueue(&(buf.buf))
			}
		default:
		}

		for s.bufs.Head() != nil && read < len(b) {
			buf := s.bufs.Head().(*[]byte)
			n := copy(b[read:], (*buf)[0:])

			if len(*buf) > len(b[read:]) {
				*buf = (*buf)[n:]
			} else {
				s.bufs.Pop()
			}

			read += n
		}

		if read > 0 || s.err != nil {
			break
		}
	}

	return read, nil
}

func (w *WorkerClient) GetOutput(ctx context.Context, id string) (<-chan OutputMessage, error) {
	newCtx := metadata.AppendToOutgoingContext(ctx, "jobid", id)
	req := &pb.GetOutputRequest{Id: id}
	stream, err := w.client.GetOutput(newCtx, req)

	if err != nil {
		return nil, errors.Wrap(err, "failed to get output stream")
	}

	ch := make(chan OutputMessage, 2)

	go readOutput(ctx, stream, ch)

	return ch, nil
}

func readOutput(ctx context.Context, stream pb.WorkerService_GetOutputClient, ch chan OutputMessage) {
	defer func() {
		close(ch)
	}()

	stdOut := make(chan byteBuf)
	stdErr := make(chan byteBuf)
	stdOutRdr := newStreamReader(stdOut)
	stdErrRdr := newStreamReader(stdErr)
	errCh := make(chan error)
	wg := &sync.WaitGroup{}

	go func() {
		err := <-errCh
		stdOut <- byteBuf{err: err}
		stdErr <- byteBuf{err: err}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdOutRdr)

		for scanner.Scan() {
			line := Line{Msg: scanner.Text(), Channel: pb.StdOut}
			ch <- OutputMessage{Output: line}
		}

		if err := scanner.Err(); err != nil {
			ch <- OutputMessage{Err: err}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdErrRdr)

		for scanner.Scan() {
			line := Line{Msg: scanner.Text(), Channel: pb.StdErr}
			ch <- OutputMessage{Output: line}
		}

		if err := scanner.Err(); err != nil {
			ch <- OutputMessage{Err: err}
		}
	}()

line:
	for {
		select {
		case <-ctx.Done():
			errCh <- io.EOF
			break line
		default:
		}

		out, err := stream.Recv()

		if err != nil {
			errCh <- err
			break
		}

		switch out.Channel {
		case pb.StdOut:
			stdOut <- byteBuf{buf: out.Output}
		default:
			stdErr <- byteBuf{buf: out.Output}
		}
	}

	wg.Wait()
}
