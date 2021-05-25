package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	pb "github.com/alexmeli100/teleport-assessment/grpc"
	"github.com/alexmeli100/teleport-assessment/grpc/client"
	"github.com/jessevdk/go-flags"
	"github.com/pkg/errors"
	"google.golang.org/grpc/credentials"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type StartCommand struct {
	client *client.WorkerClient
}

func (s *StartCommand) Execute(args []string) error {
	if len(args) < 1 {
		return errors.New("no remote command specified")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := args[0]
	var a []string

	if len(args) > 1 {
		a = args[1:]
	}

	st, err := s.client.Start(ctx, cmd, a)

	if err != nil {
		return err
	}

	printStatus(st)

	if len(opts.Output) == 0 {
		return nil
	}

	return getOutput(ctx, s.client, st.Id)
}

type StopCommand struct {
	client *client.WorkerClient
}

func (s *StopCommand) Execute(args []string) error {
	if len(args) < 1 {
		return errors.New("no job id specified")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st, err := s.client.Stop(ctx, args[0])

	if err != nil {
		return err
	}

	printStatus(st)

	return nil
}

type StatusCommand struct {
	client *client.WorkerClient
}

func (s *StatusCommand) Execute(args []string) error {
	if len(args) < 1 {
		return errors.New("no job id specified")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st, err := s.client.Status(ctx, args[0])

	if err != nil {
		return err
	}

	printStatus(st)
	return nil
}

type OutputCommand struct {
	client *client.WorkerClient
}

func (s *OutputCommand) Execute(args []string) error {
	if len(args) < 1 {
		return errors.New("no job id specified")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	return getOutput(ctx, s.client, args[0])
}

func getOutput(ctx context.Context, client *client.WorkerClient, id string) error {
	newCtx, cancel := context.WithCancel(ctx)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
	}()

	ch, err := client.GetOutput(newCtx, id)

	if err != nil {
		return err
	}

	for o := range ch {
		if o.Err == io.EOF {
			return nil
		}

		if o.Err != nil {
			return o.Err
		}

		switch o.Output.Channel {
		case pb.StdOut:
			fmt.Fprintln(os.Stdout, o.Output.Msg)
		default:
			fmt.Fprintln(os.Stderr, o.Output.Msg)
		}
	}

	return nil
}

var opts struct {
	Cert   string `long:"cert" description:"The certificate key" required:"true"`
	Key    string `long:"key" description:"The certificate key" required:"true"`
	CACert string `long:"cacert" description:"The certificate authorication certificate" required:"true"`
	Addr   string `long:"addr" description:"The server address"`
	Output []bool `short:"l" description:"Get the output of the job after starting"`
}

func main() {
	parser := flags.NewParser(&opts, flags.Default)

	_, err := parser.Parse()

	if err != nil {
		os.Exit(-1)
	}

	cred, err := loadKeyPair(opts.Key, opts.Cert, opts.CACert)

	if err != nil {
		log.Fatalf("Failed to load tls credentials, %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cl, err := client.NewWorkerClient(ctx, opts.Addr, cred)

	if err != nil {
		log.Fatalf("Failed to create worker client, %v", err)
	}

	startCmd := &StartCommand{client: cl}
	parser.AddCommand("start", "Start a process on the remote server", "", startCmd)

	stopCmd := &StopCommand{cl}
	parser.AddCommand("stop", "Stop a process on the remote server", "", stopCmd)

	statusCmd := &StatusCommand{cl}
	parser.AddCommand("status", "Get status of a process on the remote server", "", statusCmd)

	outputCmd := &OutputCommand{cl}
	parser.AddCommand("output", "Get output of a process on the remote server", "", outputCmd)

	_, err = parser.Parse()

	if err != nil {
		log.Fatal(err)
	}
}

func loadKeyPair(key, crt, ca string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(crt, key)

	if err != nil {
		return nil, err
	}

	data, err := ioutil.ReadFile(ca)

	if err != nil {
		return nil, err
	}

	capool := x509.NewCertPool()

	if !capool.AppendCertsFromPEM(data) {
		return nil, errors.New("can't add ca cert")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      capool,
		MinVersion:   3,
	}

	return credentials.NewTLS(tlsConfig), nil
}

func printStatus(s *pb.JobStatus) {
	var state string

	switch s.State {
	case pb.Fatal:
		state = "fatal"
	case pb.Finished:
		state = "finished"
	default:
		state = "running"
	}

	fmt.Printf("Job ID: %15s\n", s.Id)
	fmt.Printf("Job Status: %15s\n", state)
	fmt.Printf("Start time: %15s\n", time.Unix(0, s.StartTime))
}
