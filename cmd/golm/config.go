// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/leelsey/golm"
)

func runConfig(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "golm config: subcommand required (init|path|show|validate|edit|add-provider|rm-provider|add-agent|rm-agent|set-default)")
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, "golm config <init|path|show|validate|edit|add-provider|rm-provider|add-agent|rm-agent|set-default>")
		return 0
	case "path":
		return configPathCmd(args[1:], stdout, stderr)
	case "init":
		return configInit(args[1:], stdout, stderr)
	case "show":
		return configShow(args[1:], stdout, stderr)
	case "validate":
		return configValidate(args[1:], stdout, stderr)
	case "edit":
		return configEdit(args[1:], stdout, stderr)
	case "add-provider":
		return configAddProvider(args[1:], stdout, stderr)
	case "rm-provider":
		return configRmProvider(args[1:], stdout, stderr)
	case "add-agent":
		return configAddAgent(args[1:], stdout, stderr)
	case "rm-agent":
		return configRmAgent(args[1:], stdout, stderr)
	case "set-default":
		return configSetDefault(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "golm config: unknown subcommand %q\n", args[0])
		return 2
	}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func userConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "golm", "config.json"), nil
}

func discoverConfigPath() (string, bool) {
	if p := os.Getenv("GOLM_CONFIG"); p != "" && fileExists(p) {
		return p, true
	}
	if fileExists("golm.json") {
		return "golm.json", true
	}
	if p, err := userConfigPath(); err == nil && fileExists(p) {
		return p, true
	}
	return "", false
}

func configTargetPath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if p := os.Getenv("GOLM_CONFIG"); p != "" {
		return p, nil
	}
	if fileExists("golm.json") {
		return "golm.json", nil
	}
	return userConfigPath()
}

func loadOrEmpty(path string) (*golm.Config, error) {
	if fileExists(path) {
		return golm.LoadConfig(path)
	}
	return &golm.Config{Providers: []golm.ProviderConfig{}, Agents: []golm.PersonaConfig{}}, nil
}

func loadExisting(path string, stderr io.Writer) (*golm.Config, bool) {
	if !fileExists(path) {
		fmt.Fprintf(stderr, "golm config: %s not found (run 'golm config init')\n", path)
		return nil, false
	}
	c, err := golm.LoadConfig(path)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return nil, false
	}
	return c, true
}

func configPathCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm config path", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config path override")
	if code := parsePlain(fs, args); code >= 0 {
		return code
	}
	p, err := configTargetPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	status := "missing"
	if fileExists(p) {
		status = "exists"
	}
	fmt.Fprintf(stdout, "%s (%s)\n", p, status)
	return 0
}

func configInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm config init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config path override")
	force := fs.Bool("force", false, "overwrite an existing config")
	if code := parsePlain(fs, args); code >= 0 {
		return code
	}
	p, err := configTargetPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	if fileExists(p) && !*force {
		fmt.Fprintf(stderr, "golm config: %s already exists (use --force to overwrite)\n", p)
		return 1
	}
	c := &golm.Config{Providers: []golm.ProviderConfig{}, Agents: []golm.PersonaConfig{}}
	if err := c.Save(p); err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	fmt.Fprintf(stdout, "created %s\n", p)
	return 0
}

func configShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm config show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config path override")
	if code := parsePlain(fs, args); code >= 0 {
		return code
	}
	p, err := configTargetPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	c, ok := loadExisting(p, stderr)
	if !ok {
		return 1
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

func configValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm config validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config path override")
	if code := parsePlain(fs, args); code >= 0 {
		return code
	}
	p, err := configTargetPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	if _, ok := loadExisting(p, stderr); !ok {
		return 1
	}
	fmt.Fprintf(stdout, "%s is valid\n", p)
	return 0
}

func configEdit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm config edit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config path override")
	if code := parsePlain(fs, args); code >= 0 {
		return code
	}
	p, err := configTargetPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	c, err := loadOrEmpty(p)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	tmp := p + ".tmp"
	if err := c.Save(tmp); err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, tmp)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintln(stderr, "golm config: editor:", err)
		return 1
	}
	if _, err := golm.LoadConfig(tmp); err != nil {
		fmt.Fprintln(stderr, "golm config: edits invalid, original left unchanged:", err)
		fmt.Fprintf(stderr, "your draft is kept at %s\n", tmp)
		return 1
	}
	if err := os.Rename(tmp, p); err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	fmt.Fprintf(stdout, "saved %s\n", p)
	return 0
}

func configAddProvider(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm config add-provider", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config path override")
	name := fs.String("name", "", "provider name")
	ptype := fs.String("type", "", "anthropic|openai|google|cli")
	keyEnv := fs.String("api-key-env", "", "env var holding the API key")
	keyCmd := fs.String("api-key-cmd", "", "command that prints the API key")
	baseURL := fs.String("base-url", "", "override API base URL")
	command := fs.String("command", "", "cli backend command (type cli)")
	promptVia := fs.String("prompt-via", "", "stdin|arg (cli backend)")
	var cargs stringList
	fs.Var(&cargs, "arg", "cli backend argument (repeatable)")
	if code := parsePlain(fs, args); code >= 0 {
		return code
	}
	if *name == "" || *ptype == "" {
		fmt.Fprintln(stderr, "golm config add-provider: --name and --type are required")
		return 2
	}
	p, err := configTargetPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	c, err := loadOrEmpty(p)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	pc := golm.ProviderConfig{
		Name:      *name,
		Type:      *ptype,
		APIKeyEnv: *keyEnv,
		APIKeyCmd: *keyCmd,
		BaseURL:   *baseURL,
		Command:   *command,
		Args:      cargs,
		PromptVia: *promptVia,
	}
	verb := "added"
	replaced := false
	for i := range c.Providers {
		if c.Providers[i].Name == *name {
			c.Providers[i] = pc
			replaced = true
			verb = "updated"
			break
		}
	}
	if !replaced {
		c.Providers = append(c.Providers, pc)
	}
	if err := c.Save(p); err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s provider %q in %s\n", verb, *name, p)
	return 0
}

func configRmProvider(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm config rm-provider", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config path override")
	if code := parsePlain(fs, args); code >= 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "golm config rm-provider: exactly one provider name required")
		return 2
	}
	name := rest[0]
	p, err := configTargetPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	c, ok := loadExisting(p, stderr)
	if !ok {
		return 1
	}
	kept := make([]golm.ProviderConfig, 0, len(c.Providers))
	found := false
	for _, pr := range c.Providers {
		if pr.Name == name {
			found = true
			continue
		}
		kept = append(kept, pr)
	}
	if !found {
		fmt.Fprintf(stderr, "golm config: provider %q not found\n", name)
		return 1
	}
	c.Providers = kept
	if err := c.Save(p); err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	fmt.Fprintf(stdout, "removed provider %q from %s\n", name, p)
	return 0
}

func configAddAgent(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm config add-agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config path override")
	name := fs.String("name", "", "agent name")
	provider := fs.String("provider", "", "provider name (must exist)")
	model := fs.String("model", "", "model name")
	system := fs.String("system", "", "system prompt")
	role := fs.String("role", "", "main|sub")
	thinking := fs.String("thinking", "", "off|auto|budget")
	thinkingBudget := fs.Int("thinking-budget", 0, "thinking budget tokens")
	maxTokens := fs.Int("max-tokens", 0, "max output tokens")
	temperature := fs.Float64("temperature", 0, "sampling temperature")
	description := fs.String("description", "", "shown to the main agent when delegated")
	setDefault := fs.Bool("default", false, "also set as default_agent")
	var respModalities stringList
	fs.Var(&respModalities, "response-modality", "request non-text output: audio | image (repeatable)")
	if code := parsePlain(fs, args); code >= 0 {
		return code
	}
	if *name == "" || *provider == "" || *model == "" {
		fmt.Fprintln(stderr, "golm config add-agent: --name, --provider and --model are required")
		return 2
	}
	p, err := configTargetPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	c, err := loadOrEmpty(p)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	pa := golm.PersonaConfig{
		Name:               *name,
		Provider:           *provider,
		Model:              *model,
		System:             *system,
		Role:               *role,
		Description:        *description,
		MaxTokens:          *maxTokens,
		Temperature:        tempFlag(*temperature),
		Thinking:           *thinking,
		ThinkingBudget:     *thinkingBudget,
		ResponseModalities: respModalities,
	}
	verb := "added"
	replaced := false
	for i := range c.Agents {
		if c.Agents[i].Name == *name {
			c.Agents[i] = pa
			replaced = true
			verb = "updated"
			break
		}
	}
	if !replaced {
		c.Agents = append(c.Agents, pa)
	}
	if *setDefault {
		c.DefaultAgent = *name
	}
	if err := c.Save(p); err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s agent %q in %s\n", verb, *name, p)
	return 0
}

func tempFlag(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

func configRmAgent(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm config rm-agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config path override")
	if code := parsePlain(fs, args); code >= 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "golm config rm-agent: exactly one agent name required")
		return 2
	}
	name := rest[0]
	p, err := configTargetPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	c, ok := loadExisting(p, stderr)
	if !ok {
		return 1
	}
	kept := make([]golm.PersonaConfig, 0, len(c.Agents))
	found := false
	for _, a := range c.Agents {
		if a.Name == name {
			found = true
			continue
		}
		kept = append(kept, a)
	}
	if !found {
		fmt.Fprintf(stderr, "golm config: agent %q not found\n", name)
		return 1
	}
	c.Agents = kept
	if c.DefaultAgent == name {
		c.DefaultAgent = ""
	}
	if err := c.Save(p); err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	fmt.Fprintf(stdout, "removed agent %q from %s\n", name, p)
	return 0
}

func configSetDefault(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm config set-default", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config path override")
	if code := parsePlain(fs, args); code >= 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "golm config set-default: exactly one agent name required")
		return 2
	}
	name := rest[0]
	p, err := configTargetPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	c, ok := loadExisting(p, stderr)
	if !ok {
		return 1
	}
	c.DefaultAgent = name
	if err := c.Save(p); err != nil {
		fmt.Fprintln(stderr, "golm config:", err)
		return 1
	}
	fmt.Fprintf(stdout, "default_agent set to %q in %s\n", name, p)
	return 0
}
