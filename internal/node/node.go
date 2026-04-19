package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gopkg.in/yaml.v3"

	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/platform"
	"github.com/admin/ai_project/internal/rules"
	"github.com/admin/ai_project/internal/state"
	"github.com/admin/ai_project/pkg/contracts"
)

type Schema struct {
	Required []string `yaml:"required" json:"required"`
}

type Meta struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Dependencies []string `json:"dependencies"`
	Labels       []string `json:"labels"`
	InputSchema  Schema   `json:"input_schema"`
	OutputSchema Schema   `json:"output_schema"`
}

type CheckResult struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

type Result struct {
	Success bool              `json:"success"`
	Output  map[string]any    `json:"output"`
	Patch   state.Patch       `json:"patch"`
	Error   string            `json:"error,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

type Node interface {
	Meta() Meta
	CheckBefore(ctx context.Context, st *state.ReadOnlyState) CheckResult
	Execute(ctx context.Context, st *state.ReadOnlyState) Result
}

type Manifest struct {
	ID           string         `yaml:"id"`
	Version      string         `yaml:"version"`
	Description  string         `yaml:"description"`
	Labels       []string       `yaml:"labels"`
	Address      string         `yaml:"address"`
	AutoStart    bool           `yaml:"autostart"`
	Command      string         `yaml:"command"`
	Args         []string       `yaml:"args"`
	Handler      string         `yaml:"handler"`
	Timeout      time.Duration  `yaml:"timeout"`
	InputSchema  Schema         `yaml:"input_schema"`
	OutputSchema Schema         `yaml:"output_schema"`
	Rules        *rules.RuleSet `yaml:"rules"`
}

type Entry struct {
	Node     Node
	Rules    *rules.RuleSet
	Manifest *Manifest
}

type Registry struct {
	mu         sync.RWMutex
	builtins   map[string]Entry
	dynamic    map[string]Entry
	processes  map[string]*exec.Cmd
	watcher    *fsnotify.Watcher
	cfg        config.NodesConfig
	logger     *zap.Logger
}

func NewRegistry(cfg config.NodesConfig, logger *zap.Logger) (*Registry, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fs watcher: %w", err)
	}
	return &Registry{
		builtins:  map[string]Entry{},
		dynamic:   map[string]Entry{},
		processes: map[string]*exec.Cmd{},
		watcher:   watcher,
		cfg:       cfg,
		logger:    logger,
	}, nil
}

func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, cmd := range r.processes {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		delete(r.processes, id)
	}
	if r.watcher != nil {
		return r.watcher.Close()
	}
	return nil
}

func (r *Registry) RegisterBuiltin(n Node) error {
	meta := n.Meta()
	if meta.ID == "" {
		return errors.New("node id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.builtins[meta.ID] = Entry{Node: n}
	return nil
}

func (r *Registry) Get(id string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if entry, ok := r.dynamic[id]; ok {
		return entry, true
	}
	entry, ok := r.builtins[id]
	return entry, ok
}

func (r *Registry) List() []Meta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var metas []Meta
	for _, entry := range r.builtins {
		metas = append(metas, entry.Node.Meta())
	}
	for _, entry := range r.dynamic {
		metas = append(metas, entry.Node.Meta())
	}
	return metas
}

func (r *Registry) LoadManifests(ctx context.Context) error {
	if err := os.MkdirAll(r.cfg.ManifestDir, 0o755); err != nil {
		return fmt.Errorf("ensure manifest dir: %w", err)
	}
	files, err := os.ReadDir(r.cfg.ManifestDir)
	if err != nil {
		return fmt.Errorf("read manifest dir: %w", err)
	}
	for _, file := range files {
		if file.IsDir() || (!strings.HasSuffix(file.Name(), ".yaml") && !strings.HasSuffix(file.Name(), ".yml")) {
			continue
		}
		if err := r.loadManifest(ctx, filepath.Join(r.cfg.ManifestDir, file.Name())); err != nil {
			r.logger.Warn("load manifest failed", zap.String("file", file.Name()), zap.Error(err))
		}
	}
	if err := r.watcher.Add(r.cfg.ManifestDir); err != nil {
		return fmt.Errorf("watch manifest dir: %w", err)
	}
	go r.watch(ctx)
	return nil
}

func (r *Registry) watch(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-r.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				if err := r.loadManifest(ctx, event.Name); err != nil {
					r.logger.Warn("reload manifest failed", zap.String("file", event.Name), zap.Error(err))
				}
			}
			if event.Op&fsnotify.Remove != 0 {
				id := strings.TrimSuffix(filepath.Base(event.Name), filepath.Ext(event.Name))
				r.mu.Lock()
				for nodeID, entry := range r.dynamic {
					if entry.Manifest != nil && entry.Manifest.ID == id {
						delete(r.dynamic, nodeID)
					}
				}
				r.mu.Unlock()
			}
		case err, ok := <-r.watcher.Errors:
			if ok {
				r.logger.Warn("manifest watcher error", zap.Error(err))
			}
		}
	}
}

func (r *Registry) loadManifest(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", path, err)
	}
	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("unmarshal manifest %s: %w", path, err)
	}
	if manifest.ID == "" {
		return fmt.Errorf("manifest %s missing id", path)
	}
	if manifest.Timeout <= 0 {
		manifest.Timeout = 10 * time.Second
	}
	if manifest.AutoStart && manifest.Command != "" {
		if err := r.ensureProcess(ctx, manifest); err != nil {
			return err
		}
	}
	clientNode, err := newRemoteNode(ctx, manifest, r.cfg.GRPCDialTimeout)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dynamic[manifest.ID] = Entry{
		Node:     clientNode,
		Rules:    manifest.Rules,
		Manifest: &manifest,
	}
	return nil
}

func (r *Registry) ensureProcess(ctx context.Context, manifest Manifest) error {
	r.mu.Lock()
	if cmd, ok := r.processes[manifest.ID]; ok && cmd.Process != nil {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	cmd := exec.CommandContext(ctx, manifest.Command, manifest.Args...)
	cmd.Env = append(os.Environ(),
		"DYNAGENT_NODE_ADDRESS="+manifest.Address,
		"DYNAGENT_NODE_HANDLER="+manifest.Handler,
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start node process %s: %w", manifest.ID, err)
	}
	r.mu.Lock()
	r.processes[manifest.ID] = cmd
	r.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		r.mu.Lock()
		delete(r.processes, manifest.ID)
		r.mu.Unlock()
	}()
	return nil
}

type remoteNode struct {
	meta    Meta
	timeout time.Duration
	client  contracts.NodeRuntimeClient
	conn    *grpc.ClientConn
}

func newRemoteNode(ctx context.Context, manifest Manifest, dialTimeout time.Duration) (*remoteNode, error) {
	if manifest.Address == "" {
		return nil, fmt.Errorf("manifest %s missing address", manifest.ID)
	}
	var lastErr error
	for attempt := 1; attempt <= 8; attempt++ {
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		conn, err := grpc.DialContext(dialCtx, manifest.Address,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.CallContentSubtype(contracts.JSONCodecName)),
		)
		if err != nil {
			cancel()
			lastErr = err
			time.Sleep(250 * time.Millisecond)
			continue
		}
		client := contracts.NewNodeRuntimeClient(conn)
		metaResp, err := client.GetMeta(dialCtx, &contracts.Empty{})
		cancel()
		if err != nil {
			_ = conn.Close()
			lastErr = err
			time.Sleep(250 * time.Millisecond)
			continue
		}
		return &remoteNode{
			meta: Meta{
				ID:           metaResp.ID,
				Version:      metaResp.Version,
				Description:  metaResp.Description,
				Labels:       manifest.Labels,
				InputSchema:  manifest.InputSchema,
				OutputSchema: manifest.OutputSchema,
			},
			timeout: manifest.Timeout,
			client:  client,
			conn:    conn,
		}, nil
	}
	return nil, fmt.Errorf("fetch remote node meta %s: %w", manifest.ID, lastErr)
}

func (n *remoteNode) Meta() Meta {
	return n.meta
}

func (n *remoteNode) CheckBefore(ctx context.Context, st *state.ReadOnlyState) CheckResult {
	payload, err := st.ToMap()
	if err != nil {
		return CheckResult{Allowed: false, Reason: err.Error()}
	}
	childCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	resp, err := n.client.Check(childCtx, &contracts.CheckRequest{
		TraceID: platform.TraceIDFromContext(ctx),
		State:   payload,
	})
	if err != nil {
		return CheckResult{Allowed: false, Reason: err.Error()}
	}
	return CheckResult{Allowed: resp.Allowed, Reason: resp.Reason}
}

func (n *remoteNode) Execute(ctx context.Context, st *state.ReadOnlyState) Result {
	payload, err := st.ToMap()
	if err != nil {
		return Result{Success: false, Error: err.Error()}
	}
	childCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	resp, err := n.client.Execute(childCtx, &contracts.ExecuteRequest{
		TraceID: platform.TraceIDFromContext(ctx),
		State:   payload,
	})
	if err != nil {
		return Result{Success: false, Error: err.Error()}
	}
	var result Result
	raw, marshalErr := json.Marshal(resp.Result)
	if marshalErr != nil {
		return Result{Success: false, Error: marshalErr.Error()}
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return Result{Success: false, Error: err.Error()}
	}
	return result
}
