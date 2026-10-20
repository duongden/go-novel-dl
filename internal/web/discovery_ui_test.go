package web

import (
	"strings"
	"testing"
)

func TestDiscoverySiteTabsUseChineseShortLabels(t *testing.T) {
	data, err := templateFS.ReadFile("templates/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	script := string(data)
	for _, needle := range []string{
		`qidian: "起点"`,
		`ciweimao: "刺猬猫"`,
		`faloo: "飞卢"`,
		`fanqienovel: "番茄"`,
		`n17k: "17K"`,
		`sfacg: "SF轻文"`,
		`ciyuanji: "次元姬"`,
		`kadokado: "角角者"`,
		`alphapolis: "阿尔法"`,
		`button.title = source.name || sourceLabel(source.site)`,
		`name.textContent = discoverSiteShortLabel(source.site, source.name || sourceLabel(source.site))`,
	} {
		if !strings.Contains(script, needle) {
			t.Fatalf("expected discovery tab script to contain %s", needle)
		}
	}
}
