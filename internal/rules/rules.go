package rules

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"

	"github.com/admin/ai_project/internal/state"
)

type Rule struct {
	Expression string `yaml:"expression" json:"expression"`
	Reason     string `yaml:"reason" json:"reason"`
}

type RuleSet struct {
	Operator string    `yaml:"operator" json:"operator"`
	Rules    []Rule    `yaml:"rules" json:"rules"`
	Groups   []RuleSet `yaml:"groups" json:"groups"`
}

type Result struct {
	Allowed bool              `json:"allowed"`
	Reason  string            `json:"reason"`
	Details map[string]string `json:"details,omitempty"`
}

type Evaluator struct {
	env   *cel.Env
	cache sync.Map
}

func NewEvaluator() (*Evaluator, error) {
	env, err := cel.NewEnv(
		cel.Declarations(
			decls.NewVar("state", decls.NewMapType(decls.String, decls.Dyn)),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create cel env: %w", err)
	}
	return &Evaluator{env: env}, nil
}

func (e *Evaluator) Evaluate(ctx context.Context, set RuleSet, st *state.ReadOnlyState) Result {
	_ = ctx
	if len(set.Rules) == 0 && len(set.Groups) == 0 {
		return Result{Allowed: true}
	}
	operator := strings.ToLower(strings.TrimSpace(set.Operator))
	if operator == "" {
		operator = "and"
	}

	stateMap, err := st.ToMap()
	if err != nil {
		return Result{Allowed: false, Reason: err.Error()}
	}

	var results []Result
	for _, rule := range set.Rules {
		results = append(results, e.evaluateRule(rule, stateMap))
	}
	for _, group := range set.Groups {
		results = append(results, e.Evaluate(ctx, group, st))
	}

	switch operator {
	case "or":
		for _, result := range results {
			if result.Allowed {
				return Result{Allowed: true}
			}
		}
		return Result{Allowed: false, Reason: firstReason(results)}
	default:
		for _, result := range results {
			if !result.Allowed {
				return result
			}
		}
		return Result{Allowed: true}
	}
}

func (e *Evaluator) evaluateRule(rule Rule, stateMap map[string]any) Result {
	expr := normalizeExpression(strings.TrimSpace(rule.Expression))
	if expr == "" {
		return Result{Allowed: true}
	}
	ast, ok := e.cache.Load(expr)
	var checked *cel.Ast
	if ok {
		checked = ast.(*cel.Ast)
	} else {
		parsed, issues := e.env.Parse(expr)
		if issues != nil && issues.Err() != nil {
			return Result{Allowed: false, Reason: fmt.Sprintf("parse rule %q: %v", expr, issues.Err())}
		}
		compiled, issues := e.env.Check(parsed)
		if issues != nil && issues.Err() != nil {
			return Result{Allowed: false, Reason: fmt.Sprintf("check rule %q: %v", expr, issues.Err())}
		}
		e.cache.Store(expr, compiled)
		checked = compiled
	}

	program, err := e.env.Program(checked)
	if err != nil {
		return Result{Allowed: false, Reason: fmt.Sprintf("build program %q: %v", expr, err)}
	}
	out, _, err := program.Eval(map[string]any{"state": stateMap})
	if err != nil {
		return Result{Allowed: false, Reason: fmt.Sprintf("eval rule %q: %v", expr, err)}
	}
	allowed, ok := out.Value().(bool)
	if !ok {
		return Result{Allowed: false, Reason: fmt.Sprintf("rule %q did not return bool", expr)}
	}
	if allowed {
		return Result{Allowed: true}
	}
	reason := strings.TrimSpace(rule.Reason)
	if reason == "" {
		reason = fmt.Sprintf("rule rejected: %s", expr)
	}
	return Result{
		Allowed: false,
		Reason:  reason,
		Details: map[string]string{"expression": expr},
	}
}

func normalizeExpression(expr string) string {
	if expr == "" {
		return expr
	}
	if !strings.Contains(expr, "state.") {
		return expr
	}
	parts := strings.Split(expr, " ")
	for index, part := range parts {
		if !strings.HasPrefix(part, "state.") {
			continue
		}
		path := strings.TrimPrefix(part, "state.")
		segments := strings.Split(path, ".")
		builder := strings.Builder{}
		builder.WriteString("state")
		for _, segment := range segments {
			builder.WriteString("[\"")
			builder.WriteString(strings.TrimSpace(segment))
			builder.WriteString("\"]")
		}
		parts[index] = builder.String()
	}
	return strings.Join(parts, " ")
}

func firstReason(results []Result) string {
	for _, result := range results {
		if result.Reason != "" {
			return result.Reason
		}
	}
	return "all rules rejected"
}
