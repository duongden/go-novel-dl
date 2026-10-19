package site

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	charsetpkg "golang.org/x/net/html/charset"
)

type HTMLSite struct {
	client *http.Client
}

const defaultBrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36"

func NewHTMLSite(client *http.Client) HTMLSite {
	if client == nil {
		client = &http.Client{}
	}
	return HTMLSite{client: client}
}

func (h HTMLSite) Get(ctx context.Context, rawURL string) (string, error) {
	return h.GetWithHeaders(ctx, rawURL, nil)
}

func (h HTMLSite) GetWithHeaders(ctx context.Context, rawURL string, headers map[string]string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultBrowserUserAgent)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	for key, value := range headers {
		value = strings.TrimSpace(value)
		if value != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d for %s", resp.StatusCode, rawURL)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	contentType := resp.Header.Get("Content-Type")
	reader, err := charsetpkg.NewReader(bytes.NewReader(data), contentType)
	if err == nil {
		decoded, derr := io.ReadAll(reader)
		if derr == nil {
			return string(decoded), nil
		}
	}
	return string(data), nil
}

func parseHTML(markup string) (*html.Node, error) {
	return html.Parse(strings.NewReader(markup))
}

func metaProperty(doc *html.Node, property string) string {
	for _, node := range findAll(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "meta" {
			return false
		}
		return attrValue(n, "property") == property
	}) {
		if content := strings.TrimSpace(attrValue(node, "content")); content != "" {
			return content
		}
	}
	return ""
}

func extractCommonBookTags(doc *html.Node) []string {
	if doc == nil {
		return nil
	}

	raw := make([]string, 0, 12)
	metaTagProperties := map[string]struct{}{
		"og:novel:category": {},
		"og:novel:tag":      {},
		"og:novel:tags":     {},
		"book:tag":          {},
		"article:tag":       {},
	}
	for _, node := range findAll(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "meta"
	}) {
		if _, ok := metaTagProperties[strings.ToLower(strings.TrimSpace(attrValue(node, "property")))]; !ok {
			continue
		}
		raw = append(raw, splitCommonTagValues(attrValue(node, "content"))...)
	}

	for _, link := range findAll(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a" && strings.Contains(" "+strings.ToLower(attrValue(n, "rel"))+" ", " tag ")
	}) {
		raw = append(raw, cleanCommonBookTag(nodeText(link)))
	}

	labels := []string{"作品分类", "作品标签", "小说分类", "文库分类", "文章分类", "分类", "类别", "标签", "作品Tags"}
	for _, node := range findAll(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return false
		}
		switch n.Data {
		case "p", "li", "dd", "td":
			return true
		default:
			return false
		}
	}) {
		text := cleanText(nodeText(node))
		for _, label := range labels {
			value, ok := commonTagLabelValue(text, label)
			if !ok {
				continue
			}
			links := findAll(node, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "a"
			})
			if len(links) == 0 {
				raw = append(raw, splitCommonTagValues(value)...)
			} else {
				for _, link := range links {
					raw = append(raw, cleanCommonBookTag(nodeText(link)))
				}
			}
			break
		}
	}

	for _, container := range findAll(doc, isCommonTagContainer) {
		links := findAll(container, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a"
		})
		if len(links) == 0 || len(links) > 24 {
			continue
		}
		for _, link := range links {
			raw = append(raw, cleanCommonBookTag(nodeText(link)))
		}
	}

	return normalizeBookTags(raw)
}

func commonTagLabelValue(text, label string) (string, bool) {
	text = strings.TrimSpace(text)
	for _, separator := range []string{"：", ":"} {
		prefix := label + separator
		if strings.HasPrefix(strings.ToLower(text), strings.ToLower(prefix)) {
			return strings.TrimSpace(text[len(prefix):]), true
		}
	}
	return "", false
}

func splitCommonTagValues(value string) []string {
	value = strings.NewReplacer(
		"，", " ",
		",", " ",
		"、", " ",
		"；", " ",
		";", " ",
		"|", " ",
		"｜", " ",
		"\r", " ",
		"\n", " ",
	).Replace(value)
	parts := strings.Fields(value)
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		if tag := cleanCommonBookTag(part); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func cleanCommonBookTag(value string) string {
	value = cleanText(value)
	value = strings.TrimSpace(strings.TrimLeft(value, "#＃"))
	for _, label := range []string{"作品分类", "作品标签", "小说分类", "文库分类", "文章分类", "分类", "类别", "标签", "作品Tags"} {
		for _, separator := range []string{"：", ":"} {
			prefix := label + separator
			if strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
				value = strings.TrimSpace(value[len(prefix):])
				break
			}
		}
	}
	value = strings.TrimSpace(strings.TrimLeft(value, "#＃"))
	value = strings.TrimSpace(strings.Trim(value, "，,、；;|｜"))
	if value == "" || len([]rune(value)) > 80 {
		return ""
	}
	switch strings.ToLower(value) {
	case "更多", "更多标签", "全部标签", "查看全部", "展开", "收起", "连载", "连载中", "已完结", "完本", "状态":
		return ""
	default:
		return value
	}
}

func isCommonTagContainer(node *html.Node) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}
	switch node.Data {
	case "html", "body", "head", "script", "style":
		return false
	}
	marker := strings.ToLower(attrValue(node, "class") + " " + attrValue(node, "id"))
	for _, token := range strings.FieldsFunc(marker, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if token == "tag" || token == "tags" || token == "taglist" || token == "tagbox" || token == "tagcloud" {
			return true
		}
	}
	return false
}

func normalizeBookTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.Join(strings.Fields(tag), " ")
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, tag)
	}
	return result
}

func cleanContentParagraphs(nodes []*html.Node, isAd func(string) bool) []string {
	paragraphs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		text := cleanText(nodeTextPreserveLineBreaks(node))
		if text == "" {
			continue
		}
		if isAd != nil && isAd(text) {
			continue
		}
		paragraphs = append(paragraphs, text)
	}
	return paragraphs
}

func extractTexts(nodes []*html.Node) []string {
	items := make([]string, 0, len(nodes))
	for _, node := range nodes {
		text := cleanText(nodeTextPreserveLineBreaks(node))
		if text != "" {
			items = append(items, text)
		}
	}
	return items
}

func hasAncestorByID(n *html.Node, id string) bool {
	for current := n.Parent; current != nil; current = current.Parent {
		for _, attr := range current.Attr {
			if attr.Key == "id" && attr.Val == id {
				return true
			}
		}
	}
	return false
}

func cleanLooseTexts(node *html.Node) []string {
	if node == nil {
		return nil
	}
	parts := make([]string, 0)
	for _, text := range strings.Split(cleanText(nodeTextPreserveLineBreaks(node)), "\n") {
		text = strings.TrimSpace(text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return parts
}

func fallback(value, other string) string {
	if strings.TrimSpace(value) == "" {
		return strings.TrimSpace(other)
	}
	return strings.TrimSpace(value)
}

var multiSpaceRe = regexp.MustCompile(`\s+`)

func compactWhitespace(value string) string {
	return strings.TrimSpace(multiSpaceRe.ReplaceAllString(value, " "))
}
