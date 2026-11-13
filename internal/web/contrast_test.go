package web

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestContrastMatchesTheTable(t *testing.T) {
	props := rootProps(t, string(readStylesheet(t, "tokens.css")))
	for name, hex := range rawScale {
		if props[name] != hex {
			t.Fatalf("escala %s", name)
		}
	}
	for name, raw := range semanticRoles {
		if props[name] != "var("+raw+")" {
			t.Fatalf("papel %s", name)
		}
	}
	for _, raw := range []string{"--stone-300", "--stone-700", "--stone-800"} {
		for name, value := range props {
			if name != raw && strings.Contains(value, raw) {
				t.Fatalf("%s aponta para %s", name, raw)
			}
		}
	}
	for name, value := range props {
		if value != "var(--stone-400)" {
			continue
		}
		if name != "--color-line-strong" && name != "--color-chart-muted" {
			t.Fatalf("%s usa stone-400", name)
		}
	}
	worked := contrast("#141210", "#f3efe7")
	if math.Abs(worked-16.29) > 0.02 {
		t.Fatalf("conta do texto sobre a tela %.2f", worked)
	}
	for _, pair := range textPairs {
		got := contrast(resolveColor(t, props, pair.fg), resolveColor(t, props, pair.bg))
		if got < pair.min {
			t.Fatalf("%s / %s abaixo de %.1f", pair.fg, pair.bg, pair.min)
		}
	}
	for _, pair := range uiPairs {
		got := contrast(resolveColor(t, props, pair.fg), resolveColor(t, props, pair.bg))
		if got < 3 {
			t.Fatalf("%s / %s abaixo de 3", pair.fg, pair.bg)
		}
	}
}

func TestReducedMotionZerosTheDuration(t *testing.T) {
	css := string(readStylesheet(t, "tokens.css"))
	props := rootProps(t, css)
	if props["--duration-fast"] != "120ms" {
		t.Fatal("duração")
	}
	block := css[strings.Index(css, "@media (prefers-reduced-motion: reduce)"):]
	if !strings.Contains(block, "--duration-fast: 0ms") {
		t.Fatal("movimento reduzido")
	}
	if props["--layout-narrow"] != "40rem" {
		t.Fatal("viewport")
	}
}

func TestComponentsUseOnlySemanticTokens(t *testing.T) {
	tokens := rootProps(t, string(readStylesheet(t, "tokens.css")))
	components := string(readStylesheet(t, "components.css"))
	shell, err := files.ReadFile("shell.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{components, string(shell)} {
		for _, forbidden := range []string{"#", "rgb(", "hsl(", "--stone-", "--tide-", "--clay-", "--brick-"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("cru %s", forbidden)
			}
		}
	}
	if regexp.MustCompile(`(?i)font-size\s*:[^;]*px`).MatchString(components) {
		t.Fatal("fonte em px")
	}
	if strings.Contains(components, "stroke-width") {
		t.Fatal("traço fora do ícone")
	}
	narrow := regexp.MustCompile(`max-width:\s*([0-9.]+rem)`)
	for _, match := range narrow.FindAllStringSubmatch(components, -1) {
		if match[1] != tokens["--layout-narrow"] {
			t.Fatal("media query")
		}
	}
	withoutNarrow := narrow.ReplaceAllString(components, "")
	if regexp.MustCompile(`[0-9.]+(?:px|rem)`).MatchString(withoutNarrow) {
		t.Fatal("medida solta")
	}
	box := regexp.MustCompile(`(?i)((?:margin|padding)(?:-(?:top|right|bottom|left|inline|block|inline-start|inline-end|block-start|block-end))?|gap|row-gap|column-gap)\s*:\s*([^;]+)`)
	allowed := regexp.MustCompile(`^(?:0|var\(--(?:space|size|layout)-[a-z0-9-]+\))$`)
	for _, match := range box.FindAllStringSubmatch(components, -1) {
		for _, part := range strings.Fields(match[2]) {
			if !allowed.MatchString(part) {
				t.Fatalf("espaço %s", match[1])
			}
		}
	}
	used := regexp.MustCompile(`var\((--[a-z0-9-]+)\)`)
	for _, match := range used.FindAllStringSubmatch(components, -1) {
		if _, ok := tokens[match[1]]; !ok {
			t.Fatalf("token ausente %s", match[1])
		}
	}
	tokensAt := strings.Index(string(shell), "/assets/tokens.css")
	componentsAt := strings.Index(string(shell), "/assets/components.css")
	if tokensAt < 0 || componentsAt < 0 || tokensAt > componentsAt {
		t.Fatal("ordem do css")
	}
	if !strings.Contains(string(shell), ">Sair<") {
		t.Fatal("sair")
	}
	if strings.Contains(string(shell), "<table") || strings.Contains(string(shell), "Nenhuma máquina") {
		t.Fatal("lista")
	}
}

func readStylesheet(t *testing.T, name string) []byte {
	t.Helper()
	body, err := Stylesheet(name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func rootProps(t *testing.T, css string) map[string]string {
	t.Helper()
	block := rootBlock(css)
	if block == "" {
		t.Fatal(":root")
	}
	props := map[string]string{}
	decl := regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+)\s*:\s*([^;]+);`)
	for _, match := range decl.FindAllStringSubmatch(block, -1) {
		props[match[1]] = strings.TrimSpace(match[2])
	}
	if len(props) == 0 {
		t.Fatal("tokens")
	}
	return props
}

func rootBlock(css string) string {
	marker := strings.Index(css, ":root")
	if marker < 0 {
		return ""
	}
	open := strings.Index(css[marker:], "{")
	if open < 0 {
		return ""
	}
	start := marker + open + 1
	depth := 1
	for i := start; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[start:i]
			}
		}
	}
	return ""
}

func resolveColor(t *testing.T, props map[string]string, name string) string {
	t.Helper()
	value := props[name]
	for range 4 {
		if !strings.HasPrefix(value, "var(") || !strings.HasSuffix(value, ")") {
			break
		}
		next := strings.TrimSuffix(strings.TrimPrefix(value, "var("), ")")
		resolved, ok := props[next]
		if !ok {
			t.Fatalf("sem %s", name)
		}
		value = resolved
	}
	if !strings.HasPrefix(value, "#") {
		t.Fatalf("cor %s", name)
	}
	return value
}

func contrast(left string, right string) float64 {
	lighter := luminance(left)
	darker := luminance(right)
	if darker > lighter {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

func luminance(hex string) float64 {
	red, green, blue := parseHex(hex)
	return 0.2126*linear(red) + 0.7152*linear(green) + 0.0722*linear(blue)
}

func linear(channel float64) float64 {
	s := channel / 255
	if s <= 0.04045 {
		return s / 12.92
	}
	return math.Pow((s+0.055)/1.055, 2.4)
}

func parseHex(hex string) (float64, float64, float64) {
	value, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return float64((value >> 16) & 0xff), float64((value >> 8) & 0xff), float64(value & 0xff)
}

var rawScale = map[string]string{
	"--stone-0":   "#fffdf9",
	"--stone-25":  "#f3efe7",
	"--stone-50":  "#e7e1d6",
	"--stone-100": "#d9d1c3",
	"--stone-200": "#c4b9a6",
	"--stone-300": "#a89b86",
	"--stone-400": "#7d7263",
	"--stone-500": "#5e564b",
	"--stone-600": "#474138",
	"--stone-700": "#322e28",
	"--stone-800": "#221f1b",
	"--stone-900": "#141210",
	"--tide-100":  "#e4f0f1",
	"--tide-700":  "#1c4e57",
	"--tide-800":  "#163f46",
	"--tide-900":  "#102f35",
	"--clay-100":  "#f8efe4",
	"--clay-700":  "#7a4510",
	"--brick-100": "#f8eceb",
	"--brick-700": "#8e2d2d",
}

var semanticRoles = map[string]string{
	"--color-canvas":          "--stone-25",
	"--color-surface":         "--stone-0",
	"--color-surface-muted":   "--stone-50",
	"--color-surface-hover":   "--stone-50",
	"--color-surface-pressed": "--stone-100",
	"--color-selected":        "--tide-100",
	"--color-caution-wash":    "--clay-100",
	"--color-danger-wash":     "--brick-100",
	"--color-text":            "--stone-900",
	"--color-text-secondary":  "--stone-600",
	"--color-text-disabled":   "--stone-500",
	"--color-text-accent":     "--tide-700",
	"--color-text-caution":    "--clay-700",
	"--color-text-danger":     "--brick-700",
	"--color-on-accent":       "--stone-0",
	"--color-accent":          "--tide-700",
	"--color-accent-hover":    "--tide-800",
	"--color-accent-pressed":  "--tide-900",
	"--color-focus":           "--tide-700",
	"--color-line":            "--stone-200",
	"--color-line-strong":     "--stone-400",
	"--color-chart-current":   "--tide-700",
	"--color-chart-muted":     "--stone-400",
}

var textPairs = []struct {
	fg  string
	bg  string
	min float64
}{
	{"--color-text", "--color-canvas", 4.5},
	{"--color-text", "--color-surface", 4.5},
	{"--color-text", "--color-surface-muted", 4.5},
	{"--color-text", "--color-selected", 4.5},
	{"--color-text", "--color-caution-wash", 4.5},
	{"--color-text", "--color-danger-wash", 4.5},
	{"--color-text-secondary", "--color-canvas", 4.5},
	{"--color-text-secondary", "--color-surface", 4.5},
	{"--color-text-secondary", "--color-surface-muted", 4.5},
	{"--color-text-secondary", "--color-selected", 4.5},
	{"--color-text-secondary", "--color-caution-wash", 4.5},
	{"--color-text-secondary", "--color-danger-wash", 4.5},
	{"--color-text-accent", "--color-canvas", 4.5},
	{"--color-text-accent", "--color-surface", 4.5},
	{"--color-text-accent", "--color-surface-muted", 4.5},
	{"--color-text-accent", "--color-selected", 4.5},
	{"--color-on-accent", "--color-accent", 4.5},
	{"--color-on-accent", "--color-accent-hover", 4.5},
	{"--color-on-accent", "--color-accent-pressed", 4.5},
	{"--color-text-caution", "--color-canvas", 4.5},
	{"--color-text-caution", "--color-surface", 4.5},
	{"--color-text-caution", "--color-caution-wash", 4.5},
	{"--color-text-danger", "--color-canvas", 4.5},
	{"--color-text-danger", "--color-surface", 4.5},
	{"--color-text-danger", "--color-danger-wash", 4.5},
	{"--color-text-disabled", "--color-canvas", 4.5},
	{"--color-text-disabled", "--color-surface", 4.5},
	{"--color-text-disabled", "--color-surface-muted", 4.5},
}

var uiPairs = []struct {
	fg string
	bg string
}{
	{"--color-line-strong", "--color-canvas"},
	{"--color-line-strong", "--color-surface"},
	{"--color-focus", "--color-canvas"},
	{"--color-accent", "--color-surface"},
	{"--color-text-caution", "--color-surface"},
	{"--color-text-disabled", "--color-surface"},
	{"--color-chart-muted", "--color-canvas"},
}
