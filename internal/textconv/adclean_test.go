package textconv

import "testing"

func TestCleanAdLineRemovesObfuscatedURLs(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "mixed width tabs and question marks",
			line: ">ht\\tｐ://www?ltxsdz?ｃｏｍ.ｃoｍ http://m.ltxswu.me/book/57430/",
		},
		{
			name: "standalone full width URL",
			line: "ｈｔｔｐｓ：／／ｅｘａｍｐｌｅ．ｃｏｍ／book/1",
		},
		{
			name: "domain with inserted punctuation",
			line: "?an ?e?n ?ｅｎａ`ｃｏｍ",
		},
		{
			name: "published page marker",
			line: "发布页Ltxsdz…℃〇M更多彩",
		},
		{
			name: "email advertisement marker",
			line: "發郵件到ltxsbǎ@ＧＭＡＩＬ．￠ＯＭ╒尋╜回?",
		},
		{
			name: "obfuscated URL appended to prose",
			line: "1.哥哥，我喜欢你啊。发布页Ltxsdz…℃〇M更多彩",
			want: "1.哥哥，我喜欢你啊。",
		},
		{
			name: "cyrillic homoglyph domain appended to prose",
			line: "凌晨两点，程晓羽和大飞还在做与音乐相关的工作。шщш.LтｘＳdz.соｍ",
			want: "凌晨两点，程晓羽和大飞还在做与音乐相关的工作。",
		},
		{
			name: "width symbol and accented homoglyph domain",
			line: "程晓羽再次恢复意识，已经不知道时间过去多久。最新?地址)&nbsp;Ltxsｄz.€ǒm",
			want: "程晓羽再次恢复意识，已经不知道时间过去多久。",
		},
		{
			name: "definition tag content with mixed homoglyph suffix",
			line: "程晓羽的心里才平复一点。Ltxsｄz.€ǒm.com",
			want: "程晓羽的心里才平复一点。",
		},
		{
			name: "image split advertising slogan appended to prose",
			line: "程晓羽则在省城的电视台做音乐总监。更多彩",
			want: "程晓羽则在省城的电视台做音乐总监。",
		},
		{
			name: "published mailbox appended to prose",
			line: "走出酒吧正门，音乐声消失在脑后。『发布邮箱 Ltxs??A @ GmaiL.co??』",
			want: "走出酒吧正门，音乐声消失在脑后。",
		},
		{
			name: "email appended to prose",
			line: "有问题可以联系。發郵件到ltxsbǎ@ＧＭＡＩＬ．￠ＯＭ",
			want: "有问题可以联系。",
		},
		{
			name: "obfuscated bare domain appended after comma",
			line: "两个表情严肃的人枯坐在咖啡厅靠窗户处，?an ?e?n ?ｅｎａ`ｃｏｍ",
			want: "两个表情严肃的人枯坐在咖啡厅靠窗户处，",
		},
		{
			name: "obfuscated email inserted in the middle of prose",
			line: "顾婉清吃完饭就坐到了客厅沙发上。lтｘＳb a @ ｇＭAil.c〇ｍ本子摊开，笔握在手里，坐得笔直。",
			want: "顾婉清吃完饭就坐到了客厅沙发上。本子摊开，笔握在手里，坐得笔直。",
		},
		{
			name: "published mailbox with inserted separator",
			line: "周六晚上，顾婉清又做了四个菜。地址发、布邮箱 Līx_SBǎ@ＧＭＡＩＬ．ｃＯＭ",
			want: "周六晚上，顾婉清又做了四个菜。",
		},
		{
			name: "obfuscated email suffix embedded after prose",
			line: "“我这辈子，二十二岁相亲，三面定婚期，然后结婚，生你，上班，下班，值夜班，上手术台。郵件LīxsBǎ@gｍail.ｃｏm?.ｃom发布(地址发布邮箱：ltxsba@gmail.com 发送任意邮件即可！",
			want: "“我这辈子，二十二岁相亲，三面定婚期，然后结婚，生你，上班，下班，值夜班，上手术台。",
		},
		{
			name: "obfuscated email fragment at the start of a page",
			line: "ǎ@ＧＭＡＩＬ．￠ＯＭ╒寻╜回?",
			want: "",
		},
		{
			name: "ordinary prose is preserved",
			line: "天亮了。他说：“你好。”",
			want: "天亮了。他说：“你好。”",
		},
		{
			name: "normal numeric punctuation is preserved",
			line: "这个版本号是 1.2.3，内容正常。",
			want: "这个版本号是 1.2.3，内容正常。",
		},
		{
			name: "normal phrase containing slogan is preserved",
			line: "音乐让生活更多彩。",
			want: "音乐让生活更多彩。",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CleanAdLine(test.line); got != test.want {
				t.Fatalf("CleanAdLine() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCleanAdContentPreservesNormalLines(t *testing.T) {
	input := "第一段正文。\n\n>ht\\tｐ://www?ltxsdz?ｃｏｍ.ｃoｍ http://m.ltxswu.me/book/57430/\n第二段正文。\n1.哥哥，我喜欢你啊。发布页Ltxsdz…℃〇M更多彩\n"
	want := "第一段正文。\n\n第二段正文。\n1.哥哥，我喜欢你啊。\n"

	if got := CleanAdContent(input); got != want {
		t.Fatalf("CleanAdContent() = %q, want %q", got, want)
	}
}
