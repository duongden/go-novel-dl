package site

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// 微信读书网页端正文链路的核心算法。
//
// 请求体里的 b / c 不是裸 ID，而是网页端 e() 编码后的串；s 是对参数排序序列化后
// 计算的自定义哈希；响应体是「32 位 md5 校验头 + 1 位载荷标记 + 置换过的 base64」。
// 三者都在前端混淆代码里，这里按原实现逐条复刻，字符级行为保持一致。

var wereadDigitsOnlyPattern = regexp.MustCompile(`^\d*$`)

// wereadReaderIDPattern 匹配网页端 reader 路径里的 encodeId（e() 的输出）。
var wereadReaderIDPattern = regexp.MustCompile(`^[0-9a-z]{20,40}$`)

func wereadMD5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

// wereadEncodeID 复刻网页端 e()：md5 摘要打头，数字按 9 位一段转 hex 并用 g 分隔，
// 非数字整体转 hex，最后补一个 3 位校验尾。
func wereadEncodeID(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	digest := wereadMD5Hex(text)
	out := digest[:3]

	var tag string
	var chunks []string
	if wereadDigitsOnlyPattern.MatchString(text) {
		tag = "3"
		for i := 0; i < len(text); i += 9 {
			end := i + 9
			if end > len(text) {
				end = len(text)
			}
			n, err := strconv.ParseUint(text[i:end], 10, 64)
			if err != nil {
				return ""
			}
			chunks = append(chunks, strconv.FormatUint(n, 16))
		}
	} else {
		tag = "4"
		var sb strings.Builder
		for _, r := range text {
			sb.WriteString(strconv.FormatInt(int64(r), 16))
		}
		chunks = append(chunks, sb.String())
	}

	out += tag + "2" + digest[len(digest)-2:]
	for idx, chunk := range chunks {
		length := strconv.FormatInt(int64(len(chunk)), 16)
		if len(length) == 1 {
			length = "0" + length
		}
		out += length + chunk
		if idx < len(chunks)-1 {
			out += "g"
		}
	}
	if len(out) < 0x14 {
		out += digest[:0x14-len(out)]
	}
	return out + wereadMD5Hex(out)[:3]
}

// wereadEscapeComponent 对齐 JS 的 encodeURIComponent：
// 仅保留 A-Za-z0-9 - _ . ! ~ * ' ( ) 原样，其余按 UTF-8 做 %XX。
func wereadEscapeComponent(value string) string {
	const hexDigits = "0123456789ABCDEF"
	var sb strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			sb.WriteByte(c)
		case c == '-', c == '_', c == '.', c == '!', c == '~', c == '*', c == '\'', c == '(', c == ')':
			sb.WriteByte(c)
		default:
			sb.WriteByte('%')
			sb.WriteByte(hexDigits[c>>4])
			sb.WriteByte(hexDigits[c&0x0F])
		}
	}
	return sb.String()
}

// wereadSignQuery 把参数按 key 字典序拼成 a=1&b=2（值做 encodeURIComponent）后再算哈希。
func wereadSignQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+wereadEscapeComponent(params[key]))
	}
	return wereadSignHash(strings.Join(parts, "&"))
}

// wereadSignHash 复刻前端签名哈希：双路异或移位，最后相加转小写 hex。
func wereadSignHash(query string) string {
	n := len(query)
	if n == 0 {
		return ""
	}
	var h1, h2 uint32 = 0x15051505, 0x15051505
	for i := n - 1; i > 0; i -= 2 {
		h1 = (h1 ^ (uint32(query[i]) << uint((n-i)%0x1e))) & 0x7fffffff
		h2 = (h2 ^ (uint32(query[i-1]) << uint(i%0x1e))) & 0x7fffffff
	}
	return strconv.FormatUint(uint64(h1+h2), 16)
}

// wereadShuffleKeys 从串尾推出置换用的位置表（前端用尾部字符的二进制当四进制解析）。
func wereadShuffleKeys(payload string) []int {
	n := len(payload)
	if n < 4 {
		return nil
	}
	if n < 11 {
		return []int{0, 2}
	}
	count := (n + 9) / 10
	if count > 4 {
		count = 4
	}

	var acc strings.Builder
	for i := n - 1; i > n-1-count; i-- {
		// JS 用 char.toString(2) 再 parseInt(..., 4)：二进制位里出现 2/3 之前的部分有效。
		bits := strconv.FormatInt(int64(payload[i]), 2)
		value := 0
		for j := 0; j < len(bits); j++ {
			digit := int(bits[j] - '0')
			if digit >= 4 {
				break
			}
			value = value*4 + digit
		}
		acc.WriteString(strconv.Itoa(value))
	}

	base := n - count - 2
	if base <= 0 {
		return nil
	}
	text := acc.String()
	digitLen := len(strconv.Itoa(base))
	keys := make([]int, 0, 10)
	for i := 0; len(keys) < 10 && i+digitLen < len(text); i += digitLen {
		keys = append(keys, wereadParseSliceInt(text, i, digitLen)%base)
		keys = append(keys, wereadParseSliceInt(text, i+1, digitLen)%base)
	}
	return keys
}

func wereadParseSliceInt(text string, start, length int) int {
	if start < 0 || start+length > len(text) {
		return 0
	}
	n, err := strconv.Atoi(text[start : start+length])
	if err != nil {
		return 0
	}
	return n
}

// wereadUnshuffle 反向还原被成对交换过的 base64 串。
func wereadUnshuffle(payload string) string {
	keys := wereadShuffleKeys(payload)
	if len(keys) < 2 {
		return payload
	}
	chars := []byte(payload)
	for k := len(keys) - 1; k >= 1; k -= 2 {
		for j := 1; j >= 0; j-- {
			a := keys[k] + j
			b := keys[k-1] + j
			if a < 0 || b < 0 || a >= len(chars) || b >= len(chars) {
				continue
			}
			chars[a], chars[b] = chars[b], chars[a]
		}
	}
	return string(chars)
}

// wereadDecodeChapterPayload 还原正文响应：md5 校验头 + 标记位 + 置换 base64 + html 字节。
func wereadDecodeChapterPayload(payload string) ([]byte, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, fmt.Errorf("微信读书返回空响应")
	}
	if payload == "{}" {
		return nil, fmt.Errorf("微信读书返回空壳响应：psvts 可能已过期，需重新加载阅读页")
	}
	if len(payload) <= 0x20 {
		return []byte(payload), nil
	}

	signature := payload[:0x20]
	body := payload[0x20:]
	if checksum := strings.ToUpper(wereadMD5Hex(body)); checksum != signature {
		return nil, fmt.Errorf("微信读书正文校验失败（期望 %s，实际 %s）", checksum, signature)
	}

	inner := body[1:]
	inner = wereadUnshuffle(inner)
	if pad := (4 - len(inner)%4) % 4; pad > 0 {
		inner += strings.Repeat("=", pad)
	}
	raw, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		return nil, fmt.Errorf("微信读书正文 base64 解码失败: %w", err)
	}
	return raw, nil
}

var (
	wereadXMLDeclPattern = regexp.MustCompile(`(?is)<\?xml.*?\?>`)
	// 微信读书的章节 XHTML 常写成 <head><meta .../><title/></head>。按 HTML 词法，
	// 自闭合的 <title/> 会被当成 RCDATA 开始标签，没有 </title> 就会把整篇正文吞掉。
	// 所以解析前先整段摘掉 head 和残留 title。
	wereadHeadPattern            = regexp.MustCompile(`(?is)<head\b[^>]*>.*?</head>`)
	wereadSelfClosingTitleTag    = regexp.MustCompile(`(?is)<title\b[^>]*/>`)
	wereadPairedTitleTag         = regexp.MustCompile(`(?is)<title\b[^>]*>.*?</title>`)
	wereadCollapseBlankLineRegex = regexp.MustCompile(`\n{3,}`)
)

// wereadBlockTags 是转文本时用来切分段落（前后补换行）的块级标签。
var wereadBlockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "body": true,
	"dd": true, "div": true, "dl": true, "dt": true, "figcaption": true, "figure": true,
	"footer": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"header": true, "hr": true, "html": true, "li": true, "main": true, "nav": true,
	"ol": true, "p": true, "pre": true, "section": true, "table": true, "td": true,
	"th": true, "tr": true, "ul": true,
}

// wereadHTMLToText 把正文 XHTML（一章可能由多个小文档拼成）转成纯文本段落。
func wereadHTMLToText(markup string) string {
	markup = strings.TrimSpace(markup)
	if markup == "" {
		return ""
	}
	markup = wereadXMLDeclPattern.ReplaceAllString(markup, "")
	markup = wereadHeadPattern.ReplaceAllString(markup, "")
	markup = wereadSelfClosingTitleTag.ReplaceAllString(markup, "")
	markup = wereadPairedTitleTag.ReplaceAllString(markup, "")
	if strings.TrimSpace(markup) == "" {
		return ""
	}

	context := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(markup), context)
	if err != nil || len(nodes) == 0 {
		root, parseErr := html.Parse(strings.NewReader(markup))
		if parseErr != nil {
			return ""
		}
		nodes = []*html.Node{root}
	}

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		switch node.Type {
		case html.TextNode:
			if text := strings.Join(strings.Fields(node.Data), " "); text != "" {
				sb.WriteString(text)
			}
			return
		case html.ElementNode:
			switch node.Data {
			case "script", "style", "head", "title", "meta", "link", "img":
				return
			case "br":
				sb.WriteByte('\n')
				return
			}
			block := wereadBlockTags[node.Data]
			if block {
				sb.WriteByte('\n')
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			if block {
				sb.WriteByte('\n')
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	for _, node := range nodes {
		walk(node)
	}

	lines := strings.Split(sb.String(), "\n")
	paragraphs := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			paragraphs = append(paragraphs, line)
		}
	}
	// 空行是显式段落边界：下游 NormalizeParagraphs 不会跨空行合并，
	// 章节标题、署名这类短行因此不会被粘进正文首段。
	return wereadCollapseBlankLineRegex.ReplaceAllString(strings.Join(paragraphs, "\n\n"), "\n\n")
}

// wereadLooksLikeFullDocument 判断解出来的正文是否是完整文档（试读片段不会闭合）。
func wereadLooksLikeFullDocument(markup string) bool {
	return strings.Contains(strings.ToLower(markup), "</html>")
}
