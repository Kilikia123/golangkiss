package main

import (
	"context"
	_ "embed"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	blogv1 "golangkiss/gen/proto/blog/v1"
	"golangkiss/internal/service"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	grpcAddr = ":8080"
	httpAddr = ":8090"
)

//go:embed swagger_ui.html
var swaggerHTML string

func main() {
	svc := service.NewBlogService()

	grpcServer := grpc.NewServer()
	blogv1.RegisterBlogServiceServer(grpcServer, svc)

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("listen grpc: %v", err)
	}

	go func() {
		log.Printf("gRPC server started on %s", grpcAddr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(func(key string) (string, bool) {
			if key == "X-User-Id" || key == "x-user-id" {
				return "x-user-id", true
			}
			return runtime.DefaultHeaderMatcher(key)
		}),
		runtime.WithMetadata(func(_ context.Context, r *http.Request) metadata.MD {
			md := metadata.MD{}
			if v := r.Header.Get("X-User-Id"); v != "" {
				md.Set("x-user-id", v)
			}
			return md
		}),
	)

	err = blogv1.RegisterBlogServiceHandlerFromEndpoint(
		ctx,
		mux,
		"localhost"+grpcAddr,
		[]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
	)
	if err != nil {
		log.Fatalf("register gateway: %v", err)
	}

	rootMux := http.NewServeMux()
	rootMux.Handle("/api/", mux)
	rootMux.Handle("/swagger/", http.StripPrefix("/swagger/", http.FileServer(http.Dir("./docs/swagger"))))
	rootMux.HandleFunc("/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerHTML))
	})
	rootMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	httpServer := &http.Server{
		Addr:    httpAddr,
		Handler: rootMux,
	}

	go func() {
		log.Printf("HTTP gateway started on %s", httpAddr)
		log.Printf("Swagger UI: http://localhost%s/docs", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http serve: %v", err)
		}
	}()

	waitForShutdown(grpcServer, httpServer)
}

func waitForShutdown(grpcServer *grpc.Server, httpServer *http.Server) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch

	log.Println("shutting down...")

	grpcServer.GracefulStop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("http shutdown error: %v", err)
	}
}
