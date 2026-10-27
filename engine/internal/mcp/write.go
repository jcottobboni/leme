package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jcottobboni/leme/engine/internal/audit"
	"github.com/jcottobboni/leme/engine/internal/change"
	"github.com/jcottobboni/leme/engine/internal/redact"
)

func short(s string) string {
	s = redact.Text(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

func auditResult(err error) (string, string) {
	if err == nil {
		return "ok", ""
	}
	return "error", short(err.Error())
}

func (e *Env) proposeChange(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Context, Group, Kind, Namespace, Name, Summary string
		MergePatch                                     map[string]any `json:"merge_patch"`
	}
	if err := parse(args, &a); err != nil {
		return "", err
	}
	s, err := e.resolve(ctx, a.Context)
	if err != nil {
		return "", err
	}
	// Normalize the kind first so "secrets" or "deploy" are judged as what they are.
	r, err := findResource(s, a.Group, a.Kind)
	if err != nil {
		return "", err
	}
	ref := change.Ref{Group: r.Group, Kind: r.Kind, Namespace: a.Namespace, Name: a.Name}
	if err := change.Allowed(ref.Group, ref.Kind); err != nil {
		e.audit(audit.Entry{Action: "propose", Context: s.Context.Name, Object: ref.String(), Result: "denied", Detail: err.Error()})
		return "", err
	}
	canWrite, why := e.writesAllowed(s.Context)
	p, err := e.Changes.Propose(ctx, s, ref, a.MergePatch, a.Summary, "mcp", canWrite)
	if err != nil {
		e.audit(audit.Entry{Action: "propose", Context: s.Context.Name, Object: ref.String(), Result: "error", Detail: short(err.Error())})
		return "", err
	}
	detail := "writes disabled: diff only"
	if canWrite {
		detail = "dry-run " + map[bool]string{true: "ok", false: "failed"}[p.DryRun]
	}
	e.audit(audit.Entry{Action: "propose", Context: s.Context.Name, Object: ref.String(), Result: "ok", Detail: detail})

	v := p.View(true)
	out := map[string]any{
		"context":       s.Context.Name,
		"object":        ref,
		"summary":       a.Summary,
		"diff":          lineDiff(v.Before, v.After),
		"rbacAllowed":   v.RBACAllowed,
		"writesEnabled": canWrite,
	}
	if canWrite {
		out["dryRun"] = v.DryRun
		if v.DryRunError != "" {
			out["dryRunError"] = redact.Text(v.DryRunError)
		}
		out["token"] = v.Token
		out["expires"] = v.Expires.UTC().Format(time.RFC3339)
		out["next"] = "Show this diff and summary to the user. Call apply_change with the token only after they explicitly approve. " +
			"If dryRun is false, the API server rejected the change: do not apply, fix the patch instead."
	} else {
		out["dryRun"] = false
		out["note"] = "Not applied and not dry-run: " + why + " This diff is only a preview for the user; apply_change will be refused."
	}
	return jsonText(out)
}

func (e *Env) applyChange(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct{ Context, Token string }
	if err := parse(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Token) == "" {
		return "", errors.New("token is required")
	}
	s, err := e.resolve(ctx, a.Context)
	if err != nil {
		return "", err
	}
	obj := ""
	if pending, err := e.Changes.Get(s.Context.ID, a.Token); err == nil {
		obj = pending.Ref.String()
	}
	var denied error
	p, err := e.Changes.Apply(ctx, s, a.Token, func() error {
		if ok, why := e.writesAllowed(s.Context); !ok {
			denied = errors.New(why)
			return denied
		}
		return nil
	})
	if err != nil {
		res, detail := auditResult(err)
		if denied != nil {
			res = "denied"
		}
		e.audit(audit.Entry{Action: "apply", Context: s.Context.Name, Object: obj, Result: res, Detail: detail})
		return "", err
	}
	e.audit(audit.Entry{Action: "apply", Context: s.Context.Name, Object: p.Ref.String(), Result: "ok"})
	return jsonText(map[string]any{"applied": true, "context": s.Context.Name, "object": p.Ref, "summary": p.Summary})
}
