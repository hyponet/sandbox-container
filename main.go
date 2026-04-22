package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"os"

	"github.com/hyponet/sandbox-container/audit"
	"github.com/hyponet/sandbox-container/executor"
	"github.com/hyponet/sandbox-container/handler"
	"github.com/hyponet/sandbox-container/middleware"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

const (
	isolationModeNone    = "none"
	isolationModeBwrap   = "bwrap"
	bwrapNetworkHost     = "host"
	bwrapNetworkIsolated = "isolated"
)

func newCommandExecutor() executor.CommandExecutor {
	mode, err := parseIsolationMode(os.Getenv("SANDBOX_ISOLATION_MODE"))
	if err != nil {
		log.Fatal(err)
	}

	switch mode {
	case isolationModeBwrap:
		networkMode, err := parseBwrapNetworkMode(os.Getenv("SANDBOX_BWRAP_NETWORK"))
		if err != nil {
			log.Fatal(err)
		}
		cfg := executor.BwrapConfig{
			NetworkMode:      networkMode,
			ExtraROBinds:     splitCommaEnv("SANDBOX_BWRAP_EXTRA_RO_BINDS"),
			ProcBindFallback: os.Getenv("SANDBOX_BWRAP_PROC_BIND") != "",
		}
		bwrapExec, err := executor.NewBwrapExecutor(cfg)
		if err != nil {
			log.Fatalf("Failed to initialize bwrap executor: %v", err)
		}
		log.Println("Isolation mode: bwrap")
		return bwrapExec
	default:
		log.Println("Isolation mode: none (direct execution)")
		return &executor.DirectExecutor{}
	}
}

func parseIsolationMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		return isolationModeNone, nil
	}
	switch mode {
	case isolationModeNone, isolationModeBwrap:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid SANDBOX_ISOLATION_MODE %q: expected one of [%s, %s]", raw, isolationModeNone, isolationModeBwrap)
	}
}

func parseBwrapNetworkMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		return bwrapNetworkHost, nil
	}
	switch mode {
	case bwrapNetworkHost, bwrapNetworkIsolated:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid SANDBOX_BWRAP_NETWORK %q: expected one of [%s, %s]", raw, bwrapNetworkHost, bwrapNetworkIsolated)
	}
}

func splitCommaEnv(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	var result []string
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			result = append(result, s)
		}
	}
	return result
}

func main() {
	auditW := audit.NewWriter("/data/agents", 5*time.Minute)
	defer auditW.Close()

	mgr := session.NewManager("/data/agents", 24*time.Hour)
	mgr.SetAuditWriter(auditW)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())
	auth := middleware.AuthRequired()
	auditMW := middleware.AuditLogger(auditW)

	// Sandbox APIs (no session required, no auth for healthcheck)
	sandboxH := handler.NewSandboxHandler()
	r.GET("/v1/sandbox", sandboxH.GetContext)
	r.GET("/v1/sandbox/packages/python", sandboxH.GetPythonPackages)
	r.GET("/v1/sandbox/packages/nodejs", sandboxH.GetNodejsPackages)

	// Bash APIs
	cmdExec := newCommandExecutor()
	bashH := handler.NewBashHandler(mgr, cmdExec)
	bash := r.Group("/v1/bash", auth)
	{
		bash.POST("/exec", auditMW, bashH.Exec)
		bash.POST("/output", bashH.Output)
		bash.POST("/write", auditMW, bashH.Write)
		bash.POST("/kill", auditMW, bashH.Kill)
		bash.GET("/sessions", bashH.ListSessions)
		bash.POST("/sessions/create", auditMW, bashH.CreateSession)
		bash.POST("/sessions/:session_id/close", auditMW, bashH.CloseSession)
	}

	// File APIs
	fileOp := executor.NewFileOperator(cmdExec)
	fileH := handler.NewFileHandler(mgr, fileOp)
	f := r.Group("/v1/file", auth)
	{
		f.POST("/read", fileH.Read)
		f.POST("/write", auditMW, fileH.Write)
		f.POST("/replace", auditMW, fileH.Replace)
		f.POST("/search", fileH.Search)
		f.POST("/find", fileH.Find)
		f.POST("/grep", fileH.Grep)
		f.POST("/glob", fileH.Glob)
		f.POST("/upload", auditMW, fileH.Upload)
		f.GET("/download", fileH.Download)
		f.POST("/list", fileH.List)
	}

	// Code APIs
	codeH := handler.NewCodeHandler(mgr, cmdExec)
	r.POST("/v1/code/execute", auth, auditMW, codeH.Execute)
	r.GET("/v1/code/info", auth, codeH.Info)

	// Skills APIs
	if err := os.MkdirAll(session.DefaultGlobalSkills, 0755); err != nil {
		log.Fatalf("Failed to create global skills directory %s: %v", session.DefaultGlobalSkills, err)
	}
	if err := os.MkdirAll(session.DefaultRegistryRoot, 0755); err != nil {
		log.Fatalf("Failed to create skill registry directory %s: %v", session.DefaultRegistryRoot, err)
	}
	skillH := handler.NewSkillHandler(mgr)

	// Skill Registry APIs
	registryH := handler.NewRegistryHandler(mgr)
	registry := r.Group("/v1/registry", auth, auditMW)
	{
		// Skill-level management
		registry.POST("/create", registryH.Create)
		registry.POST("/get", registryH.Get)
		registry.POST("/update", registryH.Update)
		registry.POST("/delete", registryH.Delete)
		registry.POST("/list", registryH.List)
		registry.POST("/rename", registryH.Rename)
		registry.POST("/copy", registryH.Copy)
		registry.POST("/import", registryH.Import)
		registry.POST("/import/upload", registryH.ImportUpload)
		registry.GET("/export", registryH.Export)

		// Version management
		registry.POST("/versions/create", registryH.VersionCreate)
		registry.POST("/versions/get", registryH.VersionGet)
		registry.POST("/versions/list", registryH.VersionList)
		registry.POST("/versions/delete", registryH.VersionDelete)
		registry.POST("/versions/clone", registryH.VersionClone)
		registry.POST("/versions/tree", registryH.VersionTree)

		// Version file operations
		registry.POST("/versions/file/read", registryH.VersionFileRead)
		registry.POST("/versions/file/write", registryH.VersionFileWrite)
		registry.POST("/versions/file/update", registryH.VersionFileUpdate)
		registry.POST("/versions/file/mkdir", registryH.VersionFileMkdir)
		registry.POST("/versions/file/delete", registryH.VersionFileDelete)

		// Activate and commit
		registry.POST("/activate", registryH.Activate)
		registry.POST("/commit", registryH.Commit)
	}

	// Agent skill APIs
	agents := r.Group("/v1/skills/agents", auth, auditMW)
	{
		agents.POST("/:agent_id/list", skillH.AgentList)
		agents.POST("/:agent_id/load", skillH.AgentLoad)
		agents.DELETE("/:agent_id/cache", skillH.AgentCacheDelete)
	}

	// Session Management APIs
	sessionH := handler.NewSessionHandler(mgr)
	sess := r.Group("/v1/sessions", auth)
	{
		sess.GET("", sessionH.ListSessions)
		sess.GET("/:session_id/audits", sessionH.GetAuditLogs)
		sess.DELETE("/:session_id", sessionH.DeleteSession)
	}

	log.Println("Starting sandbox server on :9090")
	if err := r.Run(":9090"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
