package textconv

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

var adDomainSuffixRe = regexp.MustCompile(`(?i)\.(?:com|net|org|cc|me|cn|xyz|top|vip|info|io|tv|la|co|site|online|club|link|app|live)(?:[/:]|$)`)
var adBareDomainSuffixRe = regexp.MustCompile(`(?i)(?:com|net|org|cc|me|cn|xyz|top|vip|info|io|tv|la|co)(?:[/:]|$)`)

var adMarkers = []string{
	"地址发布", "地址發布", "发布地址", "發布地址", "发布页", "發布頁",
	"最新地址", "永久地址", "备用地址", "備用地址", "最新网址", "最新網址",
	"永久网址", "永久網址", "备用网址", "備用網址",
	"请截图保存", "請截圖保存", "请记住本站", "請記住本站",
	"请收藏本站", "請收藏本站", "手机访问", "手機訪問",
	"加入书签", "加入書籤", "返回目录", "返回目錄",
	"点击下一页", "點擊下一頁", "本章未完",
	"发布邮箱", "發布郵箱", "发邮件到", "發郵件到",
	"发送任意邮件", "發送任意郵件",
}

var adURLPrefixes = []string{"http://", "https://", "www."}
var adTrailingSlogans = []string{"更多精彩", "更多彩"}

type compactAdText struct {
	value      string
	offsets    []int
	obfuscated bool
}

// CleanAdContent removes standalone site advertisements and obfuscated URL
// suffixes from chapter content. Normal prose before an appended advertisement
// is retained.
func CleanAdContent(content string) string {
	if content == "" {
		return content
	}

	lines := strings.Split(content, "\n")
	cleaned := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			cleaned = append(cleaned, line)
			continue
		}
		value := CleanAdLine(line)
		if value == "" {
			changed = true
			continue
		}
		if value != line {
			changed = true
		}
		cleaned = append(cleaned, value)
	}
	if !changed {
		return content
	}
	return strings.Join(cleaned, "\n")
}

// CleanAdLine removes an obfuscated URL advertisement from one content line.
// URL ads are normally appended to a paragraph, so the prose before the first
// advertisement marker is kept while the advertisement suffix is discarded.
func CleanAdLine(line string) string {
	scan := compactAdLine(line)
	start := adMarkerOffset(scan)
	if urlStart := adURLStart(scan); urlStart >= 0 && (start < 0 || urlStart < start) {
		start = urlStart
	}
	if sloganStart := adTrailingSloganOffset(line); sloganStart >= 0 && (start < 0 || sloganStart < start) {
		start = sloganStart
	}
	if start < 0 {
		return line
	}

	prefix := strings.TrimSpace(line[:start])
	prefix = strings.TrimRightFunc(prefix, isDanglingAdRune)
	if !containsLetterOrDigit(prefix) {
		return ""
	}
	return prefix
}

func compactAdLine(value string) compactAdText {
	var builder strings.Builder
	builder.Grow(len(value))
	offsets := make([]int, 0, len(value))
	obfuscated := false

	for index, original := range value {
		folded := width.Fold.String(string(original))
		if folded != string(original) {
			obfuscated = true
		}
		for _, current := range folded {
			for _, normalized := range normalizeAdRunes(current) {
				if normalized != current {
					obfuscated = true
				}
				if isIgnoredAdRune(normalized) {
					if !unicode.IsSpace(normalized) {
						obfuscated = true
					}
					continue
				}
				before := builder.Len()
				builder.WriteRune(normalized)
				for byteIndex := before; byteIndex < builder.Len(); byteIndex++ {
					offsets = append(offsets, index)
				}
			}
		}
	}

	return compactAdText{value: builder.String(), offsets: offsets, obfuscated: obfuscated}
}

func normalizeAdRunes(value rune) []rune {
	value = unicode.ToLower(value)
	switch value {
	case '€', '¢', '©':
		return []rune{'c'}
	}
	decomposed := norm.NFD.String(string(value))
	normalized := make([]rune, 0, len(decomposed))
	for _, current := range decomposed {
		if unicode.Is(unicode.Mn, current) {
			continue
		}
		normalized = append(normalized, normalizeAdRune(current))
	}
	return normalized
}

func normalizeAdRune(value rune) rune {
	switch value {
	case '℃', '￠':
		return 'c'
	case '〇', '○', '◯':
		return 'o'
	case 'ℓ':
		return 'l'
	case 'а', 'α':
		return 'a'
	case 'в', 'β':
		return 'b'
	case 'с':
		return 'c'
	case 'ԁ', 'δ':
		return 'd'
	case 'е', 'ε', 'э':
		return 'e'
	case 'ɡ':
		return 'g'
	case 'н', 'һ':
		return 'h'
	case 'і', 'ι':
		return 'i'
	case 'ј':
		return 'j'
	case 'к', 'κ':
		return 'k'
	case 'ⅼ', 'λ':
		return 'l'
	case 'м', 'μ':
		return 'm'
	case 'о', 'ο', 'σ':
		return 'o'
	case 'р', 'ρ':
		return 'p'
	case 'ѕ':
		return 's'
	case 'т', 'τ':
		return 't'
	case 'υ':
		return 'u'
	case 'ν':
		return 'v'
	case 'ш', 'щ', 'ω':
		return 'w'
	case 'х', 'χ':
		return 'x'
	case 'у':
		return 'y'
	default:
		return value
	}
}

func isIgnoredAdRune(value rune) bool {
	if unicode.IsSpace(value) || unicode.Is(unicode.Cf, value) || unicode.Is(unicode.Mn, value) {
		return true
	}
	return strings.ContainsRune("?？·•‧∙…`´‘’ʹ゛゜\\｜", value)
}

func adMarkerOffset(scan compactAdText) int {
	if scan.value == "" {
		return -1
	}
	offset := -1
	for _, marker := range adMarkers {
		compactMarker := compactAdLine(marker).value
		if compactMarker == "" {
			continue
		}
		index := strings.Index(scan.value, compactMarker)
		if index < 0 {
			continue
		}
		candidate := scan.offsets[index]
		if offset < 0 || candidate < offset {
			offset = candidate
		}
	}
	return offset
}

func adURLStart(scan compactAdText) int {
	if scan.value == "" {
		return -1
	}

	start := -1
	for _, prefix := range adURLPrefixes {
		index := strings.Index(scan.value, prefix)
		if index < 0 {
			continue
		}
		candidate := scan.offsets[index]
		if start < 0 || candidate < start {
			start = candidate
		}
	}
	if start >= 0 {
		return start
	}

	domain := adDomainSuffixRe.FindStringIndex(scan.value)
	if domain == nil && scan.obfuscated {
		domain = adBareDomainSuffixRe.FindStringIndex(scan.value)
	}
	if domain == nil {
		return -1
	}
	domainStart := domain[0]
	for domainStart > 0 && isDomainTextByte(scan.value[domainStart-1]) {
		domainStart--
	}
	return scan.offsets[domainStart]
}

func adTrailingSloganOffset(line string) int {
	trimmed := strings.TrimSpace(line)
	for _, slogan := range adTrailingSlogans {
		if !strings.HasSuffix(trimmed, slogan) {
			continue
		}
		index := len(trimmed) - len(slogan)
		before := strings.TrimRightFunc(trimmed[:index], unicode.IsSpace)
		if !strings.ContainsRune("。！？!?；;…", lastRune(before)) {
			continue
		}
		leading := len(line) - len(strings.TrimLeftFunc(line, unicode.IsSpace))
		return leading + index
	}
	return -1
}

func isDomainTextByte(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= '0' && value <= '9' ||
		value == '-' || value == '.'
}

func isDanglingAdRune(value rune) bool {
	if unicode.IsSpace(value) || unicode.Is(unicode.Cf, value) {
		return true
	}
	return strings.ContainsRune("?？·•‧∙…`´‘’ʹ>》」』『「【（(.-—_|\\/=", value)
}

func containsLetterOrDigit(value string) bool {
	for _, current := range value {
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			return true
		}
	}
	return false
}

func lastRune(value string) rune {
	var last rune
	for _, current := range value {
		last = current
	}
	return last
}
