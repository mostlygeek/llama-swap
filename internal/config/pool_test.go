package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPoolConfig_Defaults(t *testing.T) {
	var got PoolConfig
	if err := yaml.Unmarshal([]byte("members: [a, b]\n"), &got); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if got.Spillover != 1 {
		t.Fatalf("spillover default: got %d, want 1", got.Spillover)
	}
	if len(got.Members) != 2 {
		t.Fatalf("members: got %v, want [a, b]", got.Members)
	}
}

func TestPoolConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     PoolConfig
		wantErr string
	}{
		{
			name:    "empty members",
			cfg:     PoolConfig{Spillover: 1},
			wantErr: "must not be empty",
		},
		{
			name:    "duplicate members",
			cfg:     PoolConfig{Members: []string{"a", "b", "a"}, Spillover: 1},
			wantErr: "duplicate member",
		},
		{
			name:    "spillover zero",
			cfg:     PoolConfig{Members: []string{"a"}},
			wantErr: "spillover must be >= 1",
		},
		{
			name: "valid",
			cfg:  PoolConfig{Members: []string{"a", "b", "c"}, Spillover: 2},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate("test")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate: want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestLoadConfig_PoolsValid(t *testing.T) {
	const y = `
models:
  a:
    cmd: echo a ${PORT}
    unlisted: true
  b:
    cmd: echo b ${PORT}
    unlisted: true
  c:
    cmd: echo c ${PORT}
    unlisted: true
pools:
  pool-a:
    members: [a, b, c]
    spillover: 2
groups:
  g:
    swap: false
    persistent: true
    members: [a, b, c]
`
	cfg, err := LoadConfigFromReader(strings.NewReader(y))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}
	pool, ok := cfg.Pools["pool-a"]
	if !ok {
		t.Fatal("pool-a missing from cfg.Pools")
	}
	if pool.Spillover != 2 {
		t.Fatalf("spillover: got %d, want 2", pool.Spillover)
	}
	members, ok := cfg.PoolMembers("pool-a")
	if !ok || len(members) != 3 {
		t.Fatalf("PoolMembers(pool-a): ok=%v members=%v", ok, members)
	}
}

func TestLoadConfig_PoolPreloadExpandsToFirstMember(t *testing.T) {
	const y = `
models:
  a:
    cmd: echo a ${PORT}
  b:
    cmd: echo b ${PORT}
pools:
  pool-a:
    members: [a, b]
groups:
  g: {swap: false, members: [a, b]}
hooks:
  on_startup:
    preload: [pool-a]
`
	cfg, err := LoadConfigFromReader(strings.NewReader(y))
	if err != nil {
		t.Fatalf("LoadConfigFromReader: %v", err)
	}
	got := cfg.Hooks.OnStartup.Preload
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("preload: got %v, want [a] (first pool member)", got)
	}
}

func TestLoadConfig_PoolErrors(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "member missing",
			yaml: `
models:
  a:
    cmd: echo a ${PORT}
pools:
  pool:
    members: [a, nope]
groups:
  g: {swap: false, persistent: true, members: [a]}
`,
			wantErr: `member "nope" is not a configured model`,
		},
		{
			name: "name collides with model",
			yaml: `
models:
  a:
    cmd: echo a ${PORT}
  b:
    cmd: echo b ${PORT}
pools:
  a:
    members: [b]
groups:
  g: {swap: false, persistent: true, members: [a, b]}
`,
			wantErr: `conflicts with an existing model ID`,
		},
		{
			name: "name collides with alias",
			yaml: `
models:
  a:
    cmd: echo a ${PORT}
    aliases: [poolname]
  b:
    cmd: echo b ${PORT}
pools:
  poolname:
    members: [b]
groups:
  g: {swap: false, persistent: true, members: [a, b]}
`,
			wantErr: `conflicts with an existing model alias`,
		},
		{
			name: "member in multiple pools",
			yaml: `
models:
  a:
    cmd: echo a ${PORT}
  b:
    cmd: echo b ${PORT}
pools:
  p1:
    members: [a, b]
  p2:
    members: [b]
groups:
  g: {swap: false, persistent: true, members: [a, b]}
`,
			wantErr: `member of multiple pools`,
		},
		{
			name: "members in default swap group",
			yaml: `
models:
  a:
    cmd: echo a ${PORT}
  b:
    cmd: echo b ${PORT}
pools:
  pool:
    members: [a, b]
`,
			wantErr: `swap: true`,
		},
		{
			name: "members span groups",
			yaml: `
models:
  a:
    cmd: echo a ${PORT}
  b:
    cmd: echo b ${PORT}
pools:
  pool:
    members: [a, b]
groups:
  g1: {swap: false, members: [a]}
  g2: {swap: false, members: [b]}
`,
			wantErr: `span groups`,
		},
		{
			name: "matrix routing rejected with pools",
			yaml: `
models:
  a:
    cmd: echo a ${PORT}
  b:
    cmd: echo b ${PORT}
pools:
  pool:
    members: [a, b]
matrix:
  vars:
    x: a
    y: b
  sets:
    combo: "x | y"
`,
			wantErr: `not supported with matrix routing`,
		},
		{
			name: "member with filters",
			yaml: `
models:
  a:
    cmd: echo a ${PORT}
    filters:
      stripParams: "temperature"
  b:
    cmd: echo b ${PORT}
pools:
  pool:
    members: [a, b]
groups:
  g: {swap: false, members: [a, b]}
`,
			wantErr: `defines filters/useModelName`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfigFromReader(strings.NewReader(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
