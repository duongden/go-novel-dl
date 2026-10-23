package textconv

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	contentDateLineRe      = regexp.MustCompile(`^\d{2,4}[-./年]\d{1,2}[-./月]\d{1,2}日?$`)
	contentChapterLineRe   = regexp.MustCompile(`^第[0-9零〇一二三四五六七八九十百千万两]+[章节回卷篇]`)
	contentImageLineRe     = regexp.MustCompile(`^(?:!\[|\[(?:图片|圖片|插图|插圖|\?\?)\]|<img\b)`)
	paragraphTrailingClose = "」』”’）)】》〉"
	paragraphContinuation  = "，,、；;：:。.！!？?）)】》」』”’…"
	paragraphTerminals     = "。！？!?…；;．."
	paragraphOpeningMarks  = "「『“‘\""
)

const (
	paragraphWrapMinWidth        = 16
	paragraphWrapTolerance       = 6
	paragraphWrapMinCluster      = 6
	paragraphWrapMinUnterminated = 2
)

// NormalizeParagraphs repairs chapter text that was split into fixed-width
// display lines by source sites. Adjacent fragments are joined until the
// previous line reaches a real paragraph boundary. A blank line is treated as
// an explicit paragraph break and is never joined across, so sources that
// already emit structured paragraphs keep their boundaries.
func NormalizeParagraphs(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.ReplaceAll(content, "\u00a0", " ")

	paragraphs := make([]string, 0, 32)
	block := make([]string, 0, 32)
	flush := func() {
		paragraphs = append(paragraphs, joinParagraphBlock(block)...)
		block = block[:0]
	}
	for _, raw := range strings.Split(content, "\n") {
		line := normalizeParagraphLine(raw)
		if line == "" {
			flush()
			continue
		}
		block = append(block, line)
	}
	flush()
	return strings.Join(paragraphs, "\n")
}

// joinParagraphBlock 复刻固定宽度折行的拼接规则，只在同一段落块内生效。
func joinParagraphBlock(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	wrapWidth := detectFixedWrapWidth(lines)
	paragraphs := make([]string, 0, len(lines))
	lastFragment := ""

	for _, line := range lines {
		if len(paragraphs) > 0 &&
			!isStandaloneContentLine(lastFragment) &&
			!isStandaloneContentLine(line) &&
			shouldJoinParagraph(lastFragment, line, wrapWidth) {
			paragraphs[len(paragraphs)-1] = joinParagraphLines(paragraphs[len(paragraphs)-1], line)
			lastFragment = line
			continue
		}
		paragraphs = append(paragraphs, line)
		lastFragment = line
	}
	return paragraphs
}

func normalizeParagraphLine(line string) string {
	line = strings.ReplaceAll(line, "\u00a0", " ")
	return strings.Join(strings.Fields(line), " ")
}

func isStandaloneContentLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return true
	}
	if contentDateLineRe.MatchString(line) || contentImageLineRe.MatchString(line) {
		return true
	}
	if utf8.RuneCountInString(line) <= 48 && contentChapterLineRe.MatchString(line) {
		return true
	}
	return strings.HasPrefix(line, "－－") && strings.HasSuffix(line, "完")
}

func detectFixedWrapWidth(lines []string) int {
	lengthCounts := make(map[int]int)
	unterminatedCounts := make(map[int]int)
	maxLength := 0
	eligibleLines := 0
	for _, line := range lines {
		if line == "" || isStandaloneContentLine(line) {
			continue
		}
		length := utf8.RuneCountInString(line)
		if length < paragraphWrapMinWidth {
			continue
		}
		eligibleLines++
		lengthCounts[length]++
		if !hasParagraphTerminal(line) {
			unterminatedCounts[length]++
		}
		if length > maxLength {
			maxLength = length
		}
	}

	bestWidth := 0
	bestCluster := 0
	for candidate := paragraphWrapMinWidth; candidate <= maxLength; candidate++ {
		cluster := 0
		unterminated := 0
		for length, count := range lengthCounts {
			if length > candidate || length < candidate-paragraphWrapTolerance {
				continue
			}
			cluster += count
			unterminated += unterminatedCounts[length]
		}
		if cluster < paragraphWrapMinCluster || unterminated < paragraphWrapMinUnterminated ||
			cluster*2 < eligibleLines {
			continue
		}
		if cluster > bestCluster || (cluster == bestCluster && candidate > bestWidth) {
			bestWidth = candidate
			bestCluster = cluster
		}
	}
	return bestWidth
}

func shouldJoinParagraph(previous, current string, wrapWidth int) bool {
	previous = strings.TrimSpace(previous)
	current = strings.TrimSpace(current)
	if previous == "" || current == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(current)
	if strings.ContainsRune(paragraphContinuation, first) {
		return true
	}
	if hasParagraphTerminal(previous) {
		if strings.ContainsRune(paragraphOpeningMarks, first) {
			return false
		}
		return isNearFixedWrapEdge(previous, wrapWidth)
	}
	return true
}

func isNearFixedWrapEdge(line string, wrapWidth int) bool {
	if wrapWidth <= 0 {
		return false
	}
	length := utf8.RuneCountInString(strings.TrimSpace(line))
	return length >= wrapWidth-paragraphWrapTolerance
}

func hasParagraphTerminal(value string) bool {
	value = strings.TrimSpace(value)
	for value != "" {
		last, size := utf8.DecodeLastRuneInString(value)
		if unicode.IsSpace(last) {
			value = strings.TrimSpace(value[:len(value)-size])
			continue
		}
		if strings.ContainsRune(paragraphTrailingClose, last) {
			value = strings.TrimSpace(value[:len(value)-size])
			continue
		}
		return strings.ContainsRune(paragraphTerminals, last)
	}
	return false
}

func joinParagraphLines(previous, current string) string {
	last, _ := utf8.DecodeLastRuneInString(previous)
	first, _ := utf8.DecodeRuneInString(current)
	if needsParagraphSpace(last, first) {
		return previous + " " + current
	}
	return previous + current
}

func needsParagraphSpace(left, right rune) bool {
	return left < utf8.RuneSelf && right < utf8.RuneSelf &&
		(unicode.IsLetter(left) || unicode.IsDigit(left)) &&
		(unicode.IsLetter(right) || unicode.IsDigit(right))
}
