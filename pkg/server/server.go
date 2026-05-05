package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/soheilhy/cmux"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/cvewatcher/mulval/api/v1/analysis"
	"github.com/cvewatcher/mulval/api/v1/operations"
	"github.com/cvewatcher/mulval/global"
	"github.com/cvewatcher/mulval/pkg/interceptors"
	"github.com/cvewatcher/mulval/pkg/ui"
	analysispb "github.com/cvewatcher/mulval/proto/api/v1/analysis"
)

// Server is a helper to manager an API Server.
type Server struct {
	Options

	lns        *Listeners
	grpcServer *grpc.Server
	tcpm       cmux.CMux
	httpServer *http.Server
}

// Options to configure it once for all.
type Options struct {
	Port    int
	Swagger bool
	UI      bool
}

// NewServer returns a fresh API server.
func NewServer(opts Options) *Server {
	return &Server{
		Options: opts,
	}
}

// Run the API server in backend.
// It first start the listeners then proceed to launch the connection
// multiplexers for the gRPC server and its HTTP gateway.
func (s *Server) Run(ctx context.Context) error {
	if err := s.listen(); err != nil {
		return err
	}

	// Create servers
	s.grpcServer = s.newGRPCServer()
	grpcWebServer := grpcweb.WrapServer(s.grpcServer)
	s.httpServer = s.newHTTPServer(ctx, grpcWebServer)

	// Build a multiplexer to handle gRPC or HTTP services
	s.tcpm = cmux.New(s.lns.Main)
	httpL := s.tcpm.Match( // all HTTP methods used in the API v1
		cmux.HTTP1Fast(http.MethodGet),
		cmux.HTTP1Fast(http.MethodPost),
		cmux.HTTP1Fast(http.MethodPatch),
		cmux.HTTP1Fast(http.MethodDelete),
	)
	grpcL := s.tcpm.MatchWithWriters(cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"))

	// Start servicing
	logger := global.Log()
	go func() {
		if err := s.grpcServer.Serve(grpcL); err != nil {
			logger.Error(ctx, "grpc server", zap.Error(err))
		}
	}()
	go func() {
		if err := s.httpServer.Serve(httpL); err != nil {
			errStr := err.Error()
			if !strings.Contains(errStr, "closed network connection") &&
				!strings.Contains(errStr, "mux: server closed") {
				logger.Error(ctx, "http server", zap.Error(err))
			}
		}
	}()
	go func() {
		if err := s.tcpm.Serve(); err != nil {
			errStr := err.Error()
			if !strings.Contains(errStr, "closed network connection") &&
				!strings.Contains(errStr, "mux: server closed") {
				logger.Error(ctx, "cmux", zap.Error(err))
			}
		}
	}()

	return nil
}

func (s *Server) listen() error {
	// Initiate TCP listener (overall API server listener)
	mainL, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return err
	}

	// Create HTTP->gRPC forwarder
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}
	conn, err := grpc.NewClient(fmt.Sprintf("localhost:%d", s.Port), opts...)
	if err != nil {
		return multierr.Combine(err, mainL.Close())
	}

	s.lns = &Listeners{
		Main:   mainL,
		GWConn: conn,
	}
	return nil
}

func logTraceID(ctx context.Context) logging.Fields {
	if span := trace.SpanContextFromContext(ctx); span.IsSampled() {
		return logging.Fields{"trace_id", span.TraceID().String()}
	}
	return nil
}

func (s *Server) newGRPCServer() *grpc.Server {
	// Create the gRPC server
	opts := []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			logging.UnaryServerInterceptor(interceptors.Logger(global.Log().Sub), logging.WithFieldsFromContext(logTraceID)),
			recovery.UnaryServerInterceptor(),
			interceptors.UnaryClientErrors,
		),
		grpc.ChainStreamInterceptor(
			logging.StreamServerInterceptor(interceptors.Logger(global.Log().Sub), logging.WithFieldsFromContext(logTraceID)),
			recovery.StreamServerInterceptor(),
			interceptors.StreamServerErrors,
		),
	}
	grpcServer := grpc.NewServer(opts...)

	// Register every services
	analysispb.RegisterAnalysisServiceServer(grpcServer, analysis.NewAnalyzer())
	longrunningpb.RegisterOperationsServer(grpcServer, operations.NewOperationsServer())

	return grpcServer
}

func (s *Server) newHTTPServer(ctx context.Context, grpcWebHandler http.Handler) *http.Server {
	// Create multiplexer and register it in an HTTP server
	mux := http.NewServeMux()
	httpServer := http.Server{
		Addr: fmt.Sprintf("localhost:%d", s.Port),
		Handler: &handlerSwitcher{
			handler: mux,
			contentTypeToHandler: map[string]http.Handler{
				"application/grpc-web+proto": grpcWebHandler,
			},
		},
		ReadHeaderTimeout: time.Second,
	}

	// Build gateway to the HTTP 1.1+JSON server
	gwmux := runtime.NewServeMux()

	mux.Handle("/api/v1/", otelhttp.NewHandler(gwmux, "API v1 gateway"))
	mux.Handle("/healthcheck", healthcheck(ctx))

	// Add UI if requested
	if s.UI {
		mux.Handle("/", http.RedirectHandler("/ui", http.StatusSeeOther))
		ui.Register(mux)
	}

	// Add swagger if requested
	if s.Swagger {
		addSwagger(mux)
	}

	// Register all HTTP->gRPC forwarders
	must(analysispb.RegisterAnalysisServiceHandler(ctx, gwmux, s.lns.GWConn))

	// Thin HTTP wrapper for google.longrunning.Operations since grpc-gateway
	// does not ship pre-generated handlers for it.
	opsServer := operations.NewOperationsServer()
	registerOperationsHTTP(gwmux, opsServer)

	return &httpServer
}

func (s *Server) Stop(ctx context.Context) {
	// Gracefully stop gRPC, waits for in-flight RPCs to complete.
	s.grpcServer.GracefulStop()

	// Shut down the HTTP server, waits for active connections to close.
	if err := s.httpServer.Shutdown(ctx); err != nil {
		global.Log().Error(ctx, "http server shutdown", zap.Error(err))
	}

	// Shut down TCP Mux.
	s.tcpm.Close()

	// Close the gateway connection.
	if err := s.lns.GWConn.Close(); err != nil {
		global.Log().Error(ctx, "gateway conn close", zap.Error(err))
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
