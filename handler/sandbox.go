package handler

import (
	"net/http"
	"runtime"

	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/projectdata"
	"github.com/hyponet/sandbox-container/session"
	"github.com/hyponet/sandbox-container/userdata"

	"github.com/gin-gonic/gin"
)

type SandboxHandler struct {
	mgr   *session.Manager
	udMgr *userdata.Manager
	pdMgr *projectdata.Manager
}

func NewSandboxHandler(mgr *session.Manager, udMgr *userdata.Manager, pdMgr *projectdata.Manager) *SandboxHandler {
	return &SandboxHandler{mgr: mgr, udMgr: udMgr, pdMgr: pdMgr}
}

func (h *SandboxHandler) GetContext(c *gin.Context) {
	user := getCurrentUser()
	tz := getTimezone()

	_ = getHostname // available if needed
	workspace := "/"
	resp := model.SandboxResponse{
		HomeDir:   "/",
		Workspace: &workspace,
		Version:   "1.0.0",
		Detail: model.SandboxDetail{
			System: model.SystemEnv{
				OS:            runtime.GOOS,
				OSVersion:     getOSVersion(),
				Arch:          runtime.GOARCH,
				User:          user,
				HomeDir:       "/",
				Workspace:     &workspace,
				Timezone:      tz,
				OccupiedPorts: []string{},
			},
			Runtime: model.RuntimeEnv{
				Python: []model.ToolSpec{
					{Ver: getPythonVersion(), Bin: "python3"},
				},
				NodeJS: []model.ToolSpec{
					{Ver: getNodeVersion(), Bin: "node"},
				},
			},
			Utils: []model.ToolCategory{
				{
					Category: "shell",
					Tools: []model.AvailableTool{
						{Name: "bash", Description: strPtr("Bourne Again Shell")},
					},
				},
			},
		},
	}

	c.JSON(http.StatusOK, resp)
}

func (h *SandboxHandler) GetPythonPackages(c *gin.Context) {
	pkgs, err := getInstalledPackages("pip3 list --format=json 2>/dev/null")
	if err != nil {
		c.JSON(http.StatusOK, model.APIResponse{Success: true, Data: []string{}})
		return
	}
	c.JSON(http.StatusOK, model.OkResponse(pkgs))
}

func (h *SandboxHandler) GetNodejsPackages(c *gin.Context) {
	pkgs, err := getInstalledPackages("npm list -g --json 2>/dev/null")
	if err != nil {
		c.JSON(http.StatusOK, model.APIResponse{Success: true, Data: []string{}})
		return
	}
	c.JSON(http.StatusOK, model.OkResponse(pkgs))
}

func (h *SandboxHandler) FsInfo(c *gin.Context) {
	var req model.FsInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}
	var workDir string
	directories := make(map[string]string)

	workDir = SandboxHome
	directories["skills"] = SandboxSkillsDir
	if req.UserID != "" {
		if err := h.udMgr.Touch(req.UserID); err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
			return
		}
		directories["userdata"] = SandboxUserdataDir
	}
	if req.ProjectID != "" {
		if err := h.pdMgr.Touch(req.ProjectID); err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
			return
		}
		directories["projectdata"] = SandboxProjectdataDir
	}

	c.JSON(http.StatusOK, model.OkResponse(model.FsInfoResponse{
		WorkDir:     workDir,
		Directories: directories,
	}))
}
