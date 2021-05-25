package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	pb "github.com/alexmeli100/teleport-assessment/grpc"
	server2 "github.com/alexmeli100/teleport-assessment/grpc/server"
	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpczap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"github.com/jessevdk/go-flags"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
)

var opts struct {
	Cert   string `long:"cert" description:"The certificate key" required:"true"`
	Key    string `long:"key" description:"The certificate key" required:"true"`
	CACert string `long:"cacert" description:"The certificate authorication certificate" required:"true"`
	Port   string `long:"port" description:"The port to listen on" default:"8083"`
}

var logger *zap.Logger

func main() {
	p := flags.NewParser(&opts, flags.Default)
	_, err := p.Parse()

	if err != nil {
		os.Exit(-1)
	}

	// Configure logger
	config := zap.NewDevelopmentConfig()
	config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	logger, err = config.Build()

	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}

	defer logger.Sync()

	cred, err := loadKeyPair(opts.Key, opts.Cert, opts.CACert)

	if err != nil {
		logger.Fatal("Failed to load tls credentials", zap.Error(err))
	}

	worker := server2.NewWorkerServer(logger)

	ui := grpcmiddleware.ChainUnaryServer(
		worker.AuthourizeClientUnary,
		grpczap.UnaryServerInterceptor(logger))

	si := grpcmiddleware.ChainStreamServer(
		worker.AuthorizeClientStream,
		grpczap.StreamServerInterceptor(logger))

	s := grpc.NewServer(
		grpc.Creds(cred),
		grpc.UnaryInterceptor(ui),
		grpc.StreamInterceptor(si))

	pb.RegisterWorkerServiceServer(s, worker)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	addr := ":" + opts.Port

	go func() {
		defer worker.Cleanup()

		oscall := <-sig
		logger.Info("system call", zap.Any("signal", oscall))
		cancel()
	}()

	logger.Info("exit error", zap.Error(runServer(ctx, addr, s)))
}

func runServer(ctx context.Context, addr string, server *grpc.Server) error {
	listener, err := net.Listen("tcp", addr)

	if err != nil {
		return err
	}

	errc := make(chan error, 1)

	go func() {
		if err = server.Serve(listener); err != nil {
			errc <- err
		}
	}()

	logger.Info("GRPC server running", zap.String("Address", listener.Addr().String()))

	select {
	case <-ctx.Done():
		server.GracefulStop()
	case err = <-errc:
		return err
	}

	return nil
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
		ClientAuth:   tls.RequireAndVerifyClientCert,
		Certificates: []tls.Certificate{cert},
		ClientCAs:    capool,
		MinVersion:   3,
	}

	return credentials.NewTLS(tlsConfig), nil
}
