// Package main is the main package for the WeKnora server
// It contains the main function and the entry point for the server
//
// @title           WeKnora API
// @version         1.0
// @description     WeKnora 知识库管理系统 API 文档
// @termsOfService  http://swagger.io/terms/
//
// @contact.name   WeKnora Github
// @contact.url    https://github.com/Tencent/WeKnora
//
// @BasePath  /api/v1
//
// @securityDefinitions.apikey Bearer
// @in header
// @name Authorization
// @description 用户登录认证：输入 Bearer {token} 格式的 JWT 令牌

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @description API Key 认证：空间 Key 固定访问所属空间；平台 Key 调用空间接口时需同时传 X-Tenant-ID
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/portable"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/container"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/runtime"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func main() {
	portableMode := flag.Bool("portable", false, "Run with embedded storage and durable local tasks")
	dataDir := flag.String("data-dir", "", "Writable application data directory")
	resourcesDir := flag.String("resources-dir", "", "Directory containing config, migrations and web")
	host := flag.String("host", "", "HTTP bind host")
	port := flag.Int("port", -1, "HTTP port; 0 selects an available port")
	readyFile := flag.String("ready-file", "", "Atomically publish bound address as JSON")
	exitOnStdinClose := flag.Bool("exit-on-stdin-close", false, "Gracefully stop when parent closes stdin")
	flag.Parse()
	if *port < -1 || *port > 65535 {
		fmt.Fprintln(os.Stderr, "invalid --port")
		os.Exit(1)
	}

	if *portableMode {
		if *dataDir == "" {
			fmt.Fprintln(os.Stderr, "portable mode requires --data-dir")
			os.Exit(1)
		}
		unlock, err := portable.LockData(*dataDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer unlock()
		if err := portable.Prepare(portable.Options{DataDir: *dataDir, ResourcesDir: *resourcesDir}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		handler.Edition = "standard"
	}

	if *readyFile != "" {
		if err := os.Remove(*readyFile); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	logger.ConfigureFromEnv()
	// Set Gin mode
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}
	// Mute Gin's per-route registration spam (one line per route × ~150
	// routes) — replaced by a single summary printed after router build.
	runtime.SilenceGinRouteSpam()
	// Print the env banner before container build so operators see what
	// config landed even when DB / storage init fails.
	runtime.LogStartupEnv(context.Background())
	runtime.MarkServerStarted()

	// Build dependency injection container
	c := container.BuildContainer(runtime.GetContainer())

	// One-shot bootstrap hooks (e.g. promote env-named user to system
	// admin). Best-effort: never aborts startup — see bootstrap.go.
	runStartupBootstrap(c)

	// Run application
	err := c.Invoke(func(
		cfg *config.Config,
		router *gin.Engine,
		resourceCleaner interfaces.ResourceCleaner,
		systemSettingSvc interfaces.SystemSettingService,
	) error {
		// Create HTTP server
		server := &http.Server{
			Handler: router,
		}

		if *portableMode {
			cfg.Server.Host = "127.0.0.1"
		}
		if *host != "" {
			cfg.Server.Host = *host
		}
		if *port >= 0 {
			cfg.Server.Port = *port
		}
		addr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
		listener, err := listenWithRetry(addr, 10, 300*time.Millisecond)
		if err != nil {
			return fmt.Errorf("failed to start server: %v", err)
		}

		defer listener.Close()
		if err := portable.WriteReady(*readyFile, listener.Addr().String()); err != nil {
			return fmt.Errorf("write ready file: %w", err)
		}
		if *readyFile != "" {
			defer os.Remove(*readyFile)
		}
		ctx, done := context.WithCancel(context.Background())
		defer done()

		// Start the system_settings pubsub subscriber. Runs in its own
		// goroutine and exits when ctx is cancelled at shutdown. Best-
		// effort: an error here only warns (Redis may legitimately be
		// disabled in lite-mode deployments — the service no-ops in
		// that case anyway).
		if err := systemSettingSvc.SubscribeRedis(ctx); err != nil {
			logger.Warnf(ctx, "[system_settings] subscribe failed: %v", err)
		}

		signals := make(chan os.Signal, 1)
		signal.Notify(signals, shutdownSignals...)
		defer signal.Stop(signals)
		parentClosed := make(chan struct{})
		if *exitOnStdinClose {
			go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(parentClosed) }()
		}
		go func() {
			var sig os.Signal
			select {
			case sig = <-signals:
			case <-parentClosed:
			case <-ctx.Done():
				return
			}
			logger.Infof(context.Background(), "Received signal: %v, starting server shutdown...", sig)

			// Close listener first to release port immediately,
			// so the next process can bind during our graceful drain.
			listener.Close()

			shutdownTimeout := cfg.Server.ShutdownTimeout
			if shutdownTimeout == 0 {
				shutdownTimeout = 30 * time.Second
			}
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer shutdownCancel()

			// Second signal → force close all connections immediately
			go func() {
				sig := <-signals
				logger.Warnf(context.Background(), "Received second signal: %v, forcing shutdown...", sig)
				server.Close()
			}()

			if err := server.Shutdown(shutdownCtx); err != nil {
				logger.Errorf(context.Background(), "Server forced to shutdown: %v", err)
				server.Close()
			}

			logger.Info(context.Background(), "Cleaning up resources...")
			errs := resourceCleaner.Cleanup(shutdownCtx)
			if len(errs) > 0 {
				logger.Errorf(context.Background(), "Errors occurred during resource cleanup: %v", errs)
			}
			logger.Info(context.Background(), "Server has exited")
			done()
		}()

		runtime.LogGinRouteCount(context.Background())
		logger.Infof(context.Background(), "Server is running at %s", listener.Addr().String())
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed && err != net.ErrClosed {
			return fmt.Errorf("server error: %v", err)
		}

		<-ctx.Done()
		return nil
	})
	if err != nil {
		logger.Fatalf(context.Background(), "Failed to run application: %v", err)
	}
}
