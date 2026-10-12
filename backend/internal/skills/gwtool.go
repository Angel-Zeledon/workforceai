package skills

import (
	"context"
	"fmt"
	"strings"

	"aiworkforce/backend/internal/application"
)

// ToolName is the name of the agent tool; its only action is "load".
const (
	ToolName = "skill"
	ActLoad  = "load"
)

// GatewayTool serves skill.load through the Tool Gateway as a native READ tool
// (same path as the drive's fs.read: the work loop of WORK_READ_ROUNDS). It
// changes nothing and grants nothing: it returns the text of a skill the agent
// already has, in the trusted-configuration channel.
type GatewayTool struct{ Svc *Service }

var _ application.NativeTool = (*GatewayTool)(nil)

// Name implements application.NativeTool.
func (t *GatewayTool) Name() string { return ToolName }

// SideEffect implements application.NativeTool.
func (t *GatewayTool) SideEffect(action string) (bool, bool) {
	if action == ActLoad {
		return false, true
	}
	return false, false
}

func argStr(a map[string]any, k string) string {
	s, _ := a[k].(string)
	return strings.TrimSpace(s)
}

// Assess implements application.NativeTool.
func (t *GatewayTool) Assess(ctx context.Context, c application.GatewayCall) application.NativeAssessment {
	name := argStr(c.Args, "name")
	if !ValidName(name) {
		return application.NativeAssessment{DenyCode: "invalid_args"}
	}
	if f := argStr(c.Args, "file"); f != "" {
		if _, err := CleanPath(f); err != nil {
			return application.NativeAssessment{DenyCode: "invalid_args"}
		}
	}
	if err := t.Svc.CanLoad(ctx, c.Org, c.AgentID, name); err != nil {
		return application.NativeAssessment{DenyCode: "skill_not_available"}
	}
	return application.NativeAssessment{}
}

// Run implements application.NativeTool.
func (t *GatewayTool) Run(ctx context.Context, c application.GatewayCall, _ bool) application.NativeResult {
	l, err := t.Svc.Load(ctx, c.Org, LoadCall{AgentID: c.AgentID, TaskID: c.TaskID, Name: argStr(c.Args, "name"), File: argStr(c.Args, "file")})
	if err != nil {
		code := CodeOf(err)
		if code == "" {
			code = "skill_unavailable"
		}
		return application.NativeResult{Status: "failed", Code: code, Summary: code}
	}
	sum := fmt.Sprintf("skill %s v%d loaded", l.Name, l.Version)
	if l.File != "" {
		sum += " (" + l.File + ")"
	}
	return application.NativeResult{Status: "succeeded", Summary: sum, InjectionSuspected: l.Suspect,
		Trusted: []application.TrustedBlock{{Kind: "skill", Name: l.Name, Version: l.Version, File: l.File, Text: l.Text, Files: l.Files}}}
}
