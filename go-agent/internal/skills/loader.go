package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"learn-claude-code-go/internal/tools"
)

// Skill 是磁盘上一个 SKILL.md 的索引信息。
// name 和 description 来自 YAML frontmatter；如果 name 缺失，就使用 SKILL.md 所在目录名。
type Skill struct {
	Name        string
	Description string
	Path        string
	body        string
}

// Loader 负责把“可发现的 skill 摘要”和“按需加载完整 skill”分开。
// system prompt 只使用 Descriptions()，完整 body 只有模型显式调用 load_skill 时才进入上下文。
type Loader struct {
	root   string
	skills map[string]Skill
	names  []string
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// NewLoader 递归扫描 root 下所有 SKILL.md。
// 这个函数只读磁盘和建立索引，不会主动把完整 skill 内容塞进 prompt。
func NewLoader(root string) (*Loader, error) {
	loader := &Loader{root: root, skills: map[string]Skill{}}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
			return nil
		}
		skill, err := readSkill(path)
		if err != nil {
			return err
		}
		loader.skills[skill.Name] = skill
		return nil
	})
	if err != nil {
		return nil, err
	}
	for name := range loader.skills {
		loader.names = append(loader.names, name)
	}
	sort.Strings(loader.names)
	return loader, nil
}

// Descriptions 返回给模型看的低成本目录。
// 注意这里故意不包含完整正文，避免每一轮请求都携带大量无关操作指南。
func (l *Loader) Descriptions() string {
	if l == nil || len(l.names) == 0 {
		return "(no skills available)"
	}
	lines := make([]string, 0, len(l.names))
	for _, name := range l.names {
		skill := l.skills[name]
		lines = append(lines, fmt.Sprintf("- %s: %s", skill.Name, skill.Description))
	}
	return strings.Join(lines, "\n")
}

// Load 返回完整 skill 内容，并用简单 XML 标签包起来。
// tool_result 里保留原始 markdown，模型可以按 skill 中的流程继续执行。
func (l *Loader) Load(name string) (string, error) {
	name = strings.TrimSpace(name)
	skill, ok := l.skills[name]
	if !ok {
		return "", fmt.Errorf("unknown skill: %s", name)
	}
	return fmt.Sprintf("<skill name=%q>\n<path>%s</path>\n%s\n</skill>", skill.Name, skill.Path, strings.TrimSpace(skill.body)), nil
}

// RegisterLoadSkill 把 skill loader 接入通用工具系统。
// 模型只能通过工具名和 schema 请求加载；真正读文件的动作仍由 Go harness 控制。
func RegisterLoadSkill(reg *tools.Registry, loader *Loader) {
	reg.Register(tools.Tool{
		Spec: tools.Spec("load_skill", "Load the full SKILL.md instructions for a named local skill.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Skill name from the available skills list.",
				},
			},
			"required": []string{"name"},
		}),
		Handler: func(input map[string]any) (string, error) {
			name, err := skillNameArg(input)
			if err != nil {
				return "", err
			}
			return loader.Load(name)
		},
	})
}

func readSkill(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	meta, body, err := parseSkillFile(string(data))
	if err != nil {
		return Skill{}, fmt.Errorf("parse %s: %w", path, err)
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	return Skill{
		Name:        name,
		Description: strings.TrimSpace(meta.Description),
		Path:        path,
		body:        body,
	}, nil
}

func parseSkillFile(text string) (frontmatter, string, error) {
	var meta frontmatter
	if !strings.HasPrefix(text, "---\n") {
		return meta, text, nil
	}
	rest := strings.TrimPrefix(text, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, "", fmt.Errorf("frontmatter closing --- not found")
	}
	rawMeta := rest[:end]
	body := strings.TrimLeft(rest[end+len("\n---"):], "\r\n")
	if err := yaml.Unmarshal([]byte(rawMeta), &meta); err != nil {
		return meta, "", err
	}
	return meta, body, nil
}

func skillNameArg(input map[string]any) (string, error) {
	value, ok := input["name"]
	if !ok {
		return "", fmt.Errorf("name is required")
	}
	name, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("name must be a string")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	return name, nil
}
