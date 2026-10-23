package textconv

import (
	"strings"
	"testing"
)

func TestNormalizeParagraphsRejoinsFixedWidthLines(t *testing.T) {
	input := `26-10-13
第四章山脚
这个镇子比我想象的要小。一条主街，两边是些两三层高的楼房，楼下是铺
面，楼上是住家。铺面多是杂货店、小饭馆、五金店，还有几家卖户外用品的。
空气里有一股潮湿的、
带着植物腐烂味道的气息，跟在深圳完全不一样。
「没有。
「第一次你还这么懂？」
她推开窗，看见我妈已经坐在石
阶
上了，手里拿着一杯水。`
	want := `26-10-13
第四章山脚
这个镇子比我想象的要小。一条主街，两边是些两三层高的楼房，楼下是铺面，楼上是住家。铺面多是杂货店、小饭馆、五金店，还有几家卖户外用品的。
空气里有一股潮湿的、带着植物腐烂味道的气息，跟在深圳完全不一样。
「没有。
「第一次你还这么懂？」
她推开窗，看见我妈已经坐在石阶上了，手里拿着一杯水。`

	if got := NormalizeParagraphs(input); got != want {
		t.Fatalf("NormalizeParagraphs() =\n%q\nwant:\n%q", got, want)
	}
}

func TestNormalizeParagraphsPreservesImageLinesAndEnglishSpacing(t *testing.T) {
	input := "A wrapped English\nparagraph.\n![cover](https://example.com/a.png)"
	want := "A wrapped English paragraph.\n![cover](https://example.com/a.png)"
	if got := NormalizeParagraphs(input); got != want {
		t.Fatalf("NormalizeParagraphs() = %q, want %q", got, want)
	}
}

func TestNormalizeParagraphsJoinsFixedWidthLinesEndingWithPunctuation(t *testing.T) {
	firstSentence := "她推开门看见院子里空无一人，门外传来脚步声。"
	source := strings.Repeat(firstSentence, 3) +
		strings.Repeat("门外传来风吹过青石台阶的声音，", 5) +
		"随后一切又安静下来。"
	input := wrapFixedWidth(source, len([]rune(firstSentence)))
	if firstLine, _, _ := strings.Cut(input, "\n"); !strings.HasSuffix(firstLine, "。") {
		t.Fatalf("test input must contain a visual wrap after sentence punctuation: %q", firstLine)
	}
	if got := NormalizeParagraphs(input); got != source {
		t.Fatalf("NormalizeParagraphs() =\n%q\nwant:\n%q", got, source)
	}
}

func TestNormalizeParagraphsKeepsDialogueAfterWrappedNarration(t *testing.T) {
	narration := strings.Repeat("她沿着院墙慢慢向前走，听着门外越来越近的脚步声。", 4)
	dialogue := "「是谁？」\n她停下脚步。"
	input := wrapFixedWidth(narration, 18) + "\n" + dialogue
	got := NormalizeParagraphs(input)
	if !strings.HasSuffix(got, "\n"+dialogue) {
		t.Fatalf("dialogue must remain in its own paragraph:\ninput:\n%q\n got:\n%q", input, got)
	}
}

func TestNormalizeParagraphsDoesNotJoinNormalSentenceLines(t *testing.T) {
	input := "第一句。\n第二句。\n第三句。"
	if got := NormalizeParagraphs(input); got != input {
		t.Fatalf("NormalizeParagraphs() = %q, want %q", got, input)
	}
}

// 空行是显式段落边界。来源已经按段落输出时，标题、署名这类不带句末标点的短行
// 不能被折行修复逻辑粘进下一段。
func TestNormalizeParagraphsKeepsBlankLineParagraphBreaks(t *testing.T) {
	input := "版权信息\n\n书名：测试书名\n\n作者：测试作者"
	want := "版权信息\n书名：测试书名\n作者：测试作者"
	if got := NormalizeParagraphs(input); got != want {
		t.Fatalf("NormalizeParagraphs() = %q, want %q", got, want)
	}
}

func TestNormalizeParagraphsStillJoinsInsideBlock(t *testing.T) {
	input := "第一行没有标点\n第二行继续同一段。\n\n另一段从这里开始。"
	want := "第一行没有标点第二行继续同一段。\n另一段从这里开始。"
	if got := NormalizeParagraphs(input); got != want {
		t.Fatalf("NormalizeParagraphs() = %q, want %q", got, want)
	}
}

func TestDetectFixedWrapWidthRejectsVariedParagraphLines(t *testing.T) {
	lines := []string{
		"这是一段长度不同的普通正文内容。",
		"这一段的内容稍微长一些，但仍然是一个完整段落。",
		"第三段继续使用不同的长度，不能把它识别成固定宽度折行。",
		"第四段也不应该和固定宽度排版混为一谈。",
		"第五段的内容长度继续增加，用来模拟正常的段落排版。",
		"第六段结束。",
		"第七段有完整句号。",
		"第八段同样是一个完整段落。",
	}
	if got := detectFixedWrapWidth(lines); got != 0 {
		t.Fatalf("detectFixedWrapWidth() = %d, want 0 for varied paragraphs", got)
	}
}

func wrapFixedWidth(value string, width int) string {
	runes := []rune(value)
	lines := make([]string, 0, (len(runes)+width-1)/width)
	for len(runes) > width {
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	if len(runes) > 0 {
		lines = append(lines, string(runes))
	}
	return strings.Join(lines, "\n")
}
