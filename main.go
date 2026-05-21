package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"gitee.com/unitedrhino/sandbox/internal/bootstrap"
	"gitee.com/unitedrhino/sandbox/internal/config"
	"gitee.com/unitedrhino/sandbox/internal/runtime"
	"gitee.com/unitedrhino/sandbox/internal/server"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("load sandbox config: %v", err)
	}

	_, cleanup, err := bootstrap.Prepare(cfg)
	if err != nil {
		log.Fatalf("bootstrap sandbox runtime: %v", err)
	}
	defer cleanup()

	manager := runtime.NewManager(cfg, runtime.NewOSExecutor(config.ExecOptions{
		RunnerUID:              cfg.RunnerUID,
		RunnerGID:              cfg.RunnerGID,
		ControlDir:             cfg.ControlDir,
		BuiltinSkillRoot:       cfg.RuntimeSkillCommonRoot,
		SharedSkillRoot:        cfg.RuntimeSkillSharedRoot,
		MappedSkillRoot:        cfg.RuntimeSkillMappedRoot,
		SandboxNetEnable:       cfg.EnableSandboxNet,
		MountSandboxEnable:     cfg.EnableMountSandbox,
		SandboxProxyPort:       cfg.ProxyPort,
		BlockedCIDRs:           cfg.BlockedCIDRs,
		AllowedCIDRs:           cfg.AllowedCIDRs,
		AllowedPorts:           cfg.AllowedPorts,
		AllowedInternalTargets: cfg.AllowedInternalTargets,
		CPUQuota:               cfg.CPUQuota,
		MemoryLimitMB:          cfg.MemoryLimitMB,
		MaxProcesses:           cfg.MaxProcesses,
	}))
	handler := server.NewHTTPHandler(manager)

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		_ = manager.Stop()
		_ = srv.Close()
	}()

	log.Printf("sandbox listening on %s runtime=%s clone=%s", cfg.ListenAddr, cfg.RuntimeID, cfg.CloneID)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen and serve: %v", err)
	}
}
