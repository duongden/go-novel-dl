package site

import "testing"

func TestActiveSiteMetadataProvidesCuratedDefaultTags(t *testing.T) {
	registry := NewDefaultRegistry()
	allowedTags := map[string]struct{}{
		"简体中文": {}, "繁体中文": {}, "日文": {}, "韩文": {}, "英文": {},
		"原创": {}, "转载站": {}, "免费": {}, "付费": {},
		"网络小说": {}, "轻小说": {}, "经典图书": {}, "二次元": {}, "同人二创": {}, "翻译": {},
		"男性向": {}, "女性向": {}, "成人向": {}, "NSFW": {},
		"笔趣阁": {}, "起点": {}, "阅文": {}, "飞卢": {}, "番茄小说": {}, "抖音": {},
	}

	for _, key := range registry.Keys() {
		descriptor, ok := registry.SiteDescriptor(key)
		if !ok {
			t.Fatalf("build descriptor for %s", key)
		}
		if len(descriptor.Tags) == 0 {
			t.Fatalf("expected default tags for active source %s", key)
		}
		if len(descriptor.Tags) > 7 {
			t.Fatalf("source %s has too many default tags: %v", key, descriptor.Tags)
		}
		seen := map[string]struct{}{}
		for _, tag := range descriptor.Tags {
			if _, ok := allowedTags[tag]; !ok {
				t.Fatalf("source %s has an uncurated default tag %q", key, tag)
			}
			if _, duplicate := seen[tag]; duplicate {
				t.Fatalf("source %s has duplicate default tag %q", key, tag)
			}
			seen[tag] = struct{}{}
		}
	}
}
