package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"learn-claude-code-go/internal/tools"
)

// SkillDraft 是模型请求 create_skill 时提交的草稿。
// harness 会补齐 frontmatter 和固定保存路径，模型不能直接指定任意文件位置。
type SkillDraft struct {
	Name        string
	Description string
	Content     string
	Overwrite   bool
}

// CreateResult 描述新 skill 的落盘位置。
// 返回给模型后，它可以立刻调用 load_skill 验证生成结果。
type CreateResult struct {
	Name string
	Path string
}

// Create 把一次可复用经验沉淀成 skills/<name>/SKILL.md。
// 这是 s05 的“反向路径”：load_skill 把长期知识读进上下文，create_skill 把本次经验写回长期知识库。
func (l *Loader) Create(draft SkillDraft) (CreateResult, error) {
	return l.writeSkill(draft, false)
}

// Update 只更新已经存在的 skill。
// 它和 Create 分开，是为了让模型在“沉淀新经验”和“改进已有经验”之间做清晰选择。
func (l *Loader) Update(draft SkillDraft) (CreateResult, error) {
	return l.writeSkill(draft, true)
}

func (l *Loader) writeSkill(draft SkillDraft, mustExist bool) (CreateResult, error) {
	if l == nil {
		return CreateResult{}, fmt.Errorf("skill loader is required")
	}
	name, err := normalizeSkillName(draft.Name)
	if err != nil {
		return CreateResult{}, err
	}
	description := strings.TrimSpace(draft.Description)
	if description == "" {
		return CreateResult{}, fmt.Errorf("description is required")
	}
	content := strings.TrimSpace(draft.Content)
	if content == "" {
		return CreateResult{}, fmt.Errorf("content is required")
	}

	dir := filepath.Join(l.root, name)
	path := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(path); err == nil {
		if !mustExist && !draft.Overwrite {
			return CreateResult{}, fmt.Errorf("skill already exists: %s", name)
		}
	} else if os.IsNotExist(err) {
		if mustExist {
			return CreateResult{}, fmt.Errorf("skill does not exist: %s", name)
		}
	} else {
		return CreateResult{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return CreateResult{}, err
	}
	if err := os.WriteFile(path, []byte(renderSkillFile(name, description, content)), 0o644); err != nil {
		return CreateResult{}, err
	}

	skill, err := readSkill(path)
	if err != nil {
		return CreateResult{}, err
	}
	l.skills[skill.Name] = skill
	l.rebuildNames()
	return CreateResult{Name: skill.Name, Path: skill.Path}, nil
}

// RegisterCreateSkill 注册经验沉淀工具。
// 工具层负责把模型 JSON 参数转成 SkillDraft，Create 负责所有路径和覆盖保护。
func RegisterCreateSkill(reg *tools.Registry, loader *Loader) {
	reg.Register(tools.Tool{
		Spec: tools.Spec("create_skill", "Create a new local SKILL.md for reusable workflow knowledge.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        map[string]any{"type": "string", "description": "Skill name. It will be normalized to lowercase hyphen form."},
				"description": map[string]any{"type": "string", "description": "Frontmatter description that explains when to use this skill."},
				"content":     map[string]any{"type": "string", "description": "Reusable markdown instructions for SKILL.md body."},
				"overwrite":   map[string]any{"type": "boolean", "description": "Whether to replace an existing skill with the same normalized name."},
			},
			"required": []string{"name", "description", "content"},
		}),
		Handler: func(input map[string]any) (string, error) {
			draft, err := draftFromInput(input)
			if err != nil {
				return "", err
			}
			result, err := loader.Create(draft)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Created skill %q at %s", result.Name, result.Path), nil
		},
	})
}

// RegisterUpdateSkill 注册已有 skill 的更新工具。
// 和 create_skill 分开后，模型不会因为想“优化当前 skill”而意外创建一个拼写相近的新目录。
func RegisterUpdateSkill(reg *tools.Registry, loader *Loader) {
	reg.Register(tools.Tool{
		Spec: tools.Spec("update_skill", "Update an existing local SKILL.md when reusable workflow knowledge should be improved.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        map[string]any{"type": "string", "description": "Existing skill name. It will be normalized to lowercase hyphen form."},
				"description": map[string]any{"type": "string", "description": "Updated frontmatter description that explains when to use this skill."},
				"content":     map[string]any{"type": "string", "description": "Complete replacement markdown body for the existing SKILL.md."},
			},
			"required": []string{"name", "description", "content"},
		}),
		Handler: func(input map[string]any) (string, error) {
			draft, err := draftFromInput(input)
			if err != nil {
				return "", err
			}
			result, err := loader.Update(draft)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Updated skill %q at %s", result.Name, result.Path), nil
		},
	})
}

func normalizeSkillName(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || unicode.IsSpace(r):
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			return "", fmt.Errorf("invalid skill name: use letters, numbers, spaces, underscores, or hyphens")
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	return name, nil
}

func renderSkillFile(name, description, content string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", name, description, content)
}

func draftFromInput(input map[string]any) (SkillDraft, error) {
	name, err := requiredString(input, "name")
	if err != nil {
		return SkillDraft{}, err
	}
	description, err := requiredString(input, "description")
	if err != nil {
		return SkillDraft{}, err
	}
	content, err := requiredString(input, "content")
	if err != nil {
		return SkillDraft{}, err
	}
	return SkillDraft{
		Name:        name,
		Description: description,
		Content:     content,
		Overwrite:   optionalBool(input, "overwrite"),
	}, nil
}

func requiredString(input map[string]any, name string) (string, error) {
	value, ok := input[name]
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", name)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return text, nil
}

func optionalBool(input map[string]any, name string) bool {
	value, ok := input[name]
	if !ok {
		return false
	}
	enabled, ok := value.(bool)
	return ok && enabled
}

func (l *Loader) rebuildNames() {
	l.names = l.names[:0]
	for name := range l.skills {
		l.names = append(l.names, name)
	}
	sort.Strings(l.names)
}
