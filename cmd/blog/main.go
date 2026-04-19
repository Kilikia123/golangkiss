package main

import (
	"context"
	_ "embed"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	blogv1 "golangkiss/gen/proto/blog/v1"
	"golangkiss/internal/config"
	"golangkiss/internal/service"
	postgresrepo "golangkiss/internal/storage/postgres"
	redisstore "golangkiss/internal/storage/redis"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

//go:embed swagger_ui.html
var swaggerHTML string

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx := context.Background()

	repo, err := postgresrepo.New(ctx, cfg.PostgresDSN)
	if err != nil {
		log.Fatalf("init postgres: %v", err)
	}

	redisDB, err := strconv.Atoi(cfg.RedisDB)
	if err != nil {
		log.Fatalf("parse REDIS_DB: %v", err)
	}
	redisClient := redisstore.NewClient(cfg.RedisAddr, cfg.RedisPass, redisDB)
	likesStore := redisstore.NewLikesStore(redisClient)
	if err := likesStore.Ping(ctx); err != nil {
		log.Fatalf("init redis: %v", err)
	}
	mainPageCache := redisstore.NewMainPageCache(redisClient, 30*time.Second)

	svc := service.NewBlogService(repo, likesStore, mainPageCache)

	grpcServer := grpc.NewServer()
	blogv1.RegisterBlogServiceServer(grpcServer, svc)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("listen grpc: %v", err)
	}

	go func() {
		log.Printf("gRPC server started on %s", cfg.GRPCAddr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()

	gatewayCtx, cancel := context.WithCancel(ctx)
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
		gatewayCtx,
		mux,
		"localhost"+cfg.GRPCAddr,
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
		Addr:    cfg.HTTPAddr,
		Handler: rootMux,
	}

	go func() {
		log.Printf("HTTP gateway started on %s", cfg.HTTPAddr)
		log.Printf("Swagger UI: http://localhost%s/docs", cfg.HTTPAddr)
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
