package meetings

import "testing"

func TestCleanCaptions(t *testing.T) {
	vtt := "WEBVTT\n\nNOTE made by a tool\nsecond note line\n\n1\n00:00:01.000 --> 00:00:03.000\n<v Ana>Let's start.</v>\n\ncue-b\n00:00:04.000 --> 00:00:06.000\n<v.loud Bob Díaz>We decided to wait.</v>\n<i>and more</i>\n"
	if got := CleanCaptions(vtt); got != "Ana: Let's start.\nBob Díaz: We decided to wait.\nand more" {
		t.Fatalf("vtt: %q", got)
	}
	srt := "1\r\n00:00:01,000 --> 00:00:03,000\r\nAna: I will send the quote.\r\n\r\n2\r\n00:00:04,000 --> 00:00:05,000\r\nBob: ok\r\n"
	if got := CleanCaptions(srt); got != "Ana: I will send the quote.\nBob: ok" {
		t.Fatalf("srt: %q", got)
	}
	if got := CleanCaptions("plain notes without arrows\n- a, b"); got != "plain notes without arrows\n- a, b" {
		t.Fatalf("not a caption file: %q", got)
	}
	ex := ExtractMinutes(CleanCaptions(srt))
	if len(ex.Actions) != 1 || ex.Actions[0].Owner != "Ana" {
		t.Fatalf("captions feed the extractor: %+v", ex)
	}
}

func TestSentenceSplitting(t *testing.T) {
	ex := ExtractMinutes("Decidimos subir el precio 5.5% desde enero. TODO: Ana envía la cotización")
	if len(ex.Decisions) != 1 || ex.Decisions[0] != "Decidimos subir el precio 5.5% desde enero." || len(ex.Actions) != 1 || ex.Actions[0].Text != "Ana envía la cotización" && ex.Actions[0].Owner != "Ana" {
		t.Fatalf("%+v", ex)
	}
}
