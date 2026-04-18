package main

import (
	"log"
	"time"

	"os"

	"github.com/hyponet/sandbox-container/audit"
	"github.com/hyponet/sandbox-container/handler"
	"github.com/hyponet/sandbox-container/middleware"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

func main() {
	auditW := audit.NewWriter("/data/agents", 5*time.Minute)
	defer auditW.Close()

	mgr := session.NewManager("/data/agents", 24*time.Hour)
	mgr.SetAuditWriter(auditW)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.AuditLogger(auditW))
	auth := middleware.AuthRequired()

	// Sandbox APIs (no session required, no auth for healthcheck)
	sandboxH := handler.NewSandboxHandler()
	r.GET("/v1/sandbox", sandboxH.GetContext)
	r.GET("/v1/sandbox/packages/python", sandboxH.GetPythonPackages)
	r.GET("/v1/sandbox/packages/nodejs", sandboxH.GetNodejsPackages)

	// Bash APIs
	bashH := handler.NewBashHandler(mgr)
	bash := r.Group("/v1/bash", auth)
	{
		bash.POST("/exec", bashH.Exec)
		bash.POST("/output", bashH.Output)
		bash.POST("/write", bashH.Write)
		bash.POST("/kill", bashH.Kill)
		bash.GET("/sessions", bashH.ListSessions)
		bash.POST("/sessions/create", bashH.CreateSession)
		bash.POST("/sessions/:session_id/close", bashH.CloseSession)
	}

	// File APIs
	fileH := handler.NewFileHandler(mgr)
	f := r.Group("/v1/file", auth)
	{
		f.POST("/read", fileH.Read)
		f.POST("/write", fileH.Write)
		f.POST("/replace", fileH.Replace)
		f.POST("/search", fileH.Search)
		f.POST("/find", fileH.Find)
		f.POST("/grep", fileH.Grep)
		f.POST("/glob", fileH.Glob)
		f.POST("/upload", fileH.Upload)
		f.GET("/download", fileH.Download)
		f.POST("/list", fileH.List)
	}

	// Code APIs
	codeH := handler.NewCodeHandler(mgr)
	r.POST("/v1/code/execute", auth, codeH.Execute)
	r.GET("/v1/code/info", auth, codeH.Info)

	// Skills APIs
	os.MkdirAll(session.DefaultGlobalSkills, 0755)
	skillH := handler.NewSkillHandler(mgr)
	skills := r.Group("/v1/skills", auth)
	{
		// Global skill management
		skills.POST("/create", skillH.Create)
		skills.POST("/import", skillH.Import)
		skills.POST("/list", skillH.ListGlobal)
		skills.POST("/delete", skillH.Delete)
		skills.POST("/tree", skillH.Tree)
		skills.POST("/file/read", skillH.FileRead)
		skills.POST("/file/write", skillH.FileWrite)
		skills.POST("/file/update", skillH.FileUpdate)
		skills.POST("/file/mkdir", skillH.FileMkdir)
	}

	// Agent skill APIs
	agents := r.Group("/v1/skills/agents", auth)
	{
		agents.POST("/:agent_id/list", skillH.AgentList)
		agents.POST("/:agent_id/load", skillH.AgentLoad)
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
