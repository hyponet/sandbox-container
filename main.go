package main

import (
	"log"
	"time"

	"sandbox-container/handler"
	"sandbox-container/middleware"
	"sandbox-container/session"

	"github.com/gin-gonic/gin"
)

func main() {
	mgr := session.NewManager("/data/agents", 24*time.Hour)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.AuditLogger())

	// Sandbox APIs (no session required)
	sandboxH := handler.NewSandboxHandler()
	r.GET("/v1/sandbox", sandboxH.GetContext)
	r.GET("/v1/sandbox/packages/python", sandboxH.GetPythonPackages)
	r.GET("/v1/sandbox/packages/nodejs", sandboxH.GetNodejsPackages)

	// Bash APIs
	bashH := handler.NewBashHandler(mgr)
	bash := r.Group("/v1/bash")
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
	f := r.Group("/v1/file")
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
	r.POST("/v1/code/execute", codeH.Execute)
	r.GET("/v1/code/info", codeH.Info)

	// Skills APIs
	skillH := handler.NewSkillHandler(mgr)
	skills := r.Group("/v1/skills")
	{
		skills.POST("/list", skillH.List)
		skills.POST("/load", skillH.Load)
	}

	log.Println("Starting sandbox server on :9090")
	if err := r.Run(":9090"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
