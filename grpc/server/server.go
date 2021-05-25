package server

import (
	"context"
	pb "github.com/alexmeli100/teleport-assessment/grpc"
	"github.com/alexmeli100/teleport-assessment/supervisor"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"sync"
)

type clientStore map[string]map[string]bool

type WorkerServer struct {
	access     *sync.RWMutex
	supervisor *supervisor.Supervisor
	clients    clientStore
}

func NewWorkerServer(logger *zap.Logger) *WorkerServer {
	s := supervisor.NewSupervisor(logger)

	return &WorkerServer{
		access:     &sync.RWMutex{},
		supervisor: s,
		clients:    make(clientStore),
	}
}

func (w *WorkerServer) Start(ctx context.Context, job *pb.JobRequest) (*pb.JobStatus, error) {
	clientId := ctx.Value("clientID").(string)

	s, err := w.supervisor.Start(job.Cmd, job.Args)

	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	w.access.Lock()
	defer w.access.Unlock()

	jobs, ok := w.clients[clientId]

	if !ok {
		jobs = make(map[string]bool)
		w.clients[clientId] = jobs
	}

	jobs[s.Id] = true

	return toPbStatus(s), nil
}

func (w *WorkerServer) Status(_ context.Context, req *pb.StatusRequest) (*pb.JobStatus, error) {
	s, err := w.supervisor.Status(req.Id)

	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	return toPbStatus(s), nil
}

func (w *WorkerServer) GetOutput(req *pb.GetOutputRequest, stream pb.WorkerService_GetOutputServer) error {
	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	bytes, err := w.supervisor.WatchOutput(ctx, req.Id)

	if err != nil {
		return status.Error(codes.NotFound, err.Error())
	}

	for out := range bytes {
		if err = stream.Send(toPbOutput(out)); err != nil {
			return err
		}
	}

	return nil
}

func (w *WorkerServer) Stop(_ context.Context, req *pb.StopRequest) (*pb.JobStatus, error) {
	s, err := w.supervisor.Stop(req.Id)

	if err != nil && errors.Is(err, supervisor.ErrJobNotFound) {
		return nil, status.Error(codes.NotFound, err.Error())
	} else if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return toPbStatus(s), nil
}

func (w *WorkerServer) Cleanup() {
	w.supervisor.StopAll()
}

func toPbOutput(l supervisor.LogOutput) *pb.JobOutput {
	var c pb.OutputChannel

	switch l.Type {
	case supervisor.StdOut:
		c = pb.StdOut
	default:
		c = pb.StdErr
	}

	return &pb.JobOutput{Output: l.Bytes, Channel: c}
}

func (w *WorkerServer) AuthourizeClientUnary(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler) (interface{}, error) {
	newCtx, err := w.authorize(ctx, info.FullMethod)

	if err != nil {
		return nil, err
	}

	return handler(newCtx, req)
}

func (w *WorkerServer) AuthorizeClientStream(
	srv interface{},
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	_, err := w.authorize(ss.Context(), info.FullMethod)

	if err != nil {
		return err
	}

	return handler(srv, ss)
}

func (w *WorkerServer) authorize(ctx context.Context, method string) (context.Context, error) {
	p, ok := peer.FromContext(ctx)

	if !ok {
		return nil, status.Error(codes.NotFound, "client does not exist")
	}

	tlsAuth, ok := p.AuthInfo.(credentials.TLSInfo)

	if !ok {
		return nil, status.Error(codes.NotFound, "client does not exist")
	}

	if len(tlsAuth.State.VerifiedChains) == 0 || len(tlsAuth.State.VerifiedChains[0]) == 0 {
		return nil, status.Error(codes.NotFound, "could not verify peer certificate")
	}

	cn := tlsAuth.State.VerifiedChains[0][0].Subject.CommonName

	if method == "/pb.WorkerService/Start" {
		newCtx := context.WithValue(ctx, "clientID", cn)
		return newCtx, nil
	}

	md, ok := metadata.FromIncomingContext(ctx)

	if !ok {
		return nil, status.Error(codes.NotFound, "missing metadata")
	}

	jobId, ok := md["jobid"]

	if !ok {
		return nil, status.Error(codes.NotFound, "missing jobid in metadata")
	}

	if !w.ownsJob(cn, jobId[0]) {
		return nil, status.Error(codes.NotFound, "job not found for client")
	}

	return ctx, nil
}

func (w *WorkerServer) ownsJob(client, jobId string) bool {
	w.access.RLock()
	defer w.access.RUnlock()
	jobs, ok := w.clients[client]

	if !ok {
		return false
	}

	_, ok = jobs[jobId]

	return ok
}

func toPbStatus(j *supervisor.JobStatus) *pb.JobStatus {
	var s pb.JobState

	switch j.Status.State {
	case supervisor.Finished:
		s = pb.Finished
	case supervisor.Fatal:
		s = pb.Fatal
	default:
		s = pb.Running
	}

	err := ""

	if j.Status.Error != nil {
		err = j.Status.Error.Error()
	}

	return &pb.JobStatus{
		Id:        j.Id,
		PID:       int64(j.Status.PID),
		Cmd:       j.Status.Cmd,
		State:     s,
		ExitCode:  int64(j.Status.ExitCode),
		Error:     err,
		StartTime: j.Status.StartTime.UnixNano(),
		StopTime:  j.Status.StopTime.UnixNano(),
	}
}
