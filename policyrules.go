// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Decision is what a PolicyRules evaluation concluded about one tool call.
type Decision string

const (
	// DecisionAllow runs the call.
	DecisionAllow Decision = "allow"
	// DecisionDeny refuses it and tells the model why.
	DecisionDeny Decision = "deny"

	// DecisionAsk puts it to an Approver.
	DecisionAsk Decision = "ask"
)

// ErrNoApprover is DecisionAsk with nobody to ask.
var ErrNoApprover = fmt.Errorf("%w: needs approval and no approver is attached", ErrToolDenied)

// Approver is asked to approve one tool call.
type Approver func(ctx context.Context, req ToolRequest) (bool, error)

// PolicyRules is a declarative ToolPolicy.
type PolicyRules struct {
	Default Decision

	Unreviewed Decision

	Deny  []string
	Ask   []string
	Allow []string

	DenyTraits []string
	AskTraits  []string

	Approve Approver

	Audit func(req ToolRequest, d Decision, err error)
}

var traitNames = []string{"read_only", "filesystem", "network", "process"}

// ValidDecision reports whether s names a decision, accepting "" as unset.
func ValidDecision(s string) bool {
	switch Decision(strings.ToLower(strings.TrimSpace(s))) {
	case "", DecisionAllow, DecisionDeny, DecisionAsk:
		return true
	}
	return false
}

// ValidTrait reports whether s names a trait.
func ValidTrait(s string) bool {
	return slices.Contains(traitNames, strings.ToLower(strings.TrimSpace(s)))
}

func hasTrait(tr ToolTraits, name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_only":
		return tr.ReadOnly
	case "filesystem":
		return tr.Filesystem
	case "network":
		return tr.Network
	case "process":
		return tr.Process
	}
	return false
}

func anyTrait(tr ToolTraits, names []string) bool {
	for _, n := range names {
		if hasTrait(tr, n) {
			return true
		}
	}
	return false
}

// Decide evaluates the rules for one call, without asking anybody.
func (p *PolicyRules) Decide(req ToolRequest) (Decision, string) {
	name := req.Call.Name
	switch {
	case slices.Contains(p.Deny, name):
		return DecisionDeny, fmt.Sprintf("%q is denied by name", name)
	case slices.Contains(p.Ask, name):
		return DecisionAsk, fmt.Sprintf("%q needs approval by name", name)
	case slices.Contains(p.Allow, name):
		return DecisionAllow, ""
	}
	tr, declared := TraitsOf(req.Tool)
	if declared {
		if anyTrait(tr, p.DenyTraits) {
			return DecisionDeny, fmt.Sprintf("%q has a denied capability (%s)", name, strings.Join(p.DenyTraits, ", "))
		}
		if anyTrait(tr, p.AskTraits) {
			return DecisionAsk, fmt.Sprintf("%q has a capability that needs approval (%s)", name, strings.Join(p.AskTraits, ", "))
		}
	} else if p.Unreviewed != "" {
		return p.Unreviewed, fmt.Sprintf("%q declares no capabilities", name)
	}
	if p.Default == "" {
		return DecisionAllow, ""
	}
	return p.Default, "no rule matched"
}

// Policy compiles the rules into a ToolPolicy.
func (p *PolicyRules) Policy() ToolPolicy {
	if p == nil {
		return nil
	}
	asking := make(chan struct{}, 1)
	return func(ctx context.Context, req ToolRequest) (context.Context, error) {
		d, why := p.Decide(req)
		err := p.resolve(ctx, asking, req, d, why)
		p.audit(req, d, err)
		if err != nil {
			return nil, err
		}
		return ctx, nil
	}
}

func (p *PolicyRules) audit(req ToolRequest, d Decision, err error) {
	if p.Audit == nil {
		return
	}
	defer func() { _ = recover() }()
	p.Audit(req, d, err)
}

func (p *PolicyRules) resolve(ctx context.Context, asking chan struct{}, req ToolRequest, d Decision, why string) error {
	switch d {
	case DecisionAllow:
		return nil
	case DecisionDeny:
		return fmt.Errorf("%w: %s", ErrToolDenied, why)
	case DecisionAsk:
		if p.Approve == nil {
			return fmt.Errorf("%w (%s)", ErrNoApprover, why)
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case asking <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		ok, err := p.ask(ctx, req)
		<-asking
		if err != nil {
			return fmt.Errorf("%w: approval failed: %v", ErrToolDenied, err)
		}
		if !ok {
			return fmt.Errorf("%w: refused by the operator", ErrToolDenied)
		}
		return nil
	default:

		return fmt.Errorf("%w: unknown decision %q", ErrToolDenied, d)
	}
}

func (p *PolicyRules) ask(ctx context.Context, req ToolRequest) (ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			ok, err = false, fmt.Errorf("the approver panicked: %v", r)
		}
	}()
	return p.Approve(ctx, req)
}
