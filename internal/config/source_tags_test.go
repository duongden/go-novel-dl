package config

import (
	"reflect"
	"testing"
)

func TestSourceTagsSeedPreservesUserChanges(t *testing.T) {
	resetSiteCatalogForTest(t)

	defaults := map[string][]string{
		"esjzone": {"简体中文", "轻小说", "转载站"},
		"qidian":  {"简体中文", "原创", "网络小说"},
	}
	seeded, err := EnsureSourceTags(defaults, nil)
	if err != nil {
		t.Fatalf("seed source tags: %v", err)
	}
	if !reflect.DeepEqual(seeded["esjzone"], defaults["esjzone"]) {
		t.Fatalf("unexpected seeded tags: %v", seeded["esjzone"])
	}

	saved, err := UpdateSourceTags("esjzone", []string{"自定义", " 神作 ", "自定义", ""})
	if err != nil {
		t.Fatalf("update source tags: %v", err)
	}
	if !reflect.DeepEqual(saved.Tags, []string{"自定义", "神作"}) {
		t.Fatalf("expected normalized tags, got %v", saved.Tags)
	}

	reloaded, err := EnsureSourceTags(map[string][]string{
		"esjzone": {"新的默认标签"},
		"biquge5": {"简体中文"},
	}, nil)
	if err != nil {
		t.Fatalf("reload source tags: %v", err)
	}
	if !reflect.DeepEqual(reloaded["esjzone"], []string{"自定义", "神作"}) {
		t.Fatalf("expected user tags to survive reseeding, got %v", reloaded["esjzone"])
	}
	if !reflect.DeepEqual(reloaded["biquge5"], []string{"简体中文"}) {
		t.Fatalf("expected new default tags to be seeded, got %v", reloaded["biquge5"])
	}
}

func TestSourceTagsMigrationResetsBundledTagsAndPreservesLegacyEdits(t *testing.T) {
	resetSiteCatalogForTest(t)
	if err := ensureSiteCatalogDB(); err != nil {
		t.Fatalf("initialize site catalog: %v", err)
	}

	legacyRows := []sourceTagEntry{
		{SiteKey: "esjzone", Tags: `["旧默认标签"]`},
		{SiteKey: "qidian", Tags: `["我的标签"]`},
	}
	if err := siteCatalogDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("seed legacy source tags: %v", err)
	}

	saved, err := EnsureSourceTags(
		map[string][]string{
			"esjzone": {"当前默认标签"},
			"qidian":  {"当前默认标签"},
		},
		map[string][]string{
			"esjzone": {"旧默认标签"},
			"qidian":  {"旧默认标签"},
		},
	)
	if err != nil {
		t.Fatalf("migrate source tags: %v", err)
	}
	if !reflect.DeepEqual(saved["esjzone"], []string{"当前默认标签"}) {
		t.Fatalf("expected bundled tags to be updated, got %v", saved["esjzone"])
	}
	if !reflect.DeepEqual(saved["qidian"], []string{"我的标签"}) {
		t.Fatalf("expected legacy custom tags to be preserved, got %v", saved["qidian"])
	}

	var custom sourceTagEntry
	if err := siteCatalogDB.Where("site_key = ?", "qidian").Take(&custom).Error; err != nil {
		t.Fatalf("load migrated custom tags: %v", err)
	}
	if !custom.IsCustom || custom.DefaultVersion != sourceTagDefaultsVersion {
		t.Fatalf("expected legacy edit to be marked custom, got %+v", custom)
	}
}

func TestSourceTagsAllowEmptyAndRejectInvalidValues(t *testing.T) {
	resetSiteCatalogForTest(t)

	empty, err := UpdateSourceTags("qidian", nil)
	if err != nil {
		t.Fatalf("save empty tags: %v", err)
	}
	if empty.Tags == nil || len(empty.Tags) != 0 {
		t.Fatalf("expected an empty tag list, got %#v", empty.Tags)
	}

	if _, err := UpdateSourceTags("", []string{"标签"}); err == nil {
		t.Fatal("expected an empty site key to be rejected")
	}
	if _, err := UpdateSourceTags("qidian", []string{"这个标签名称明显超过了允许的最大字符长度因此应该被拒绝"}); err == nil {
		t.Fatal("expected a long tag to be rejected")
	}
}
