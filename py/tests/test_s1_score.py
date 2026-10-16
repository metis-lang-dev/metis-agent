"""s1-score.py's form fixes (specialist S1b, SPECIALIST §5b): a padded
phrase is a word on its padded side, stems still match, quotes around a
name do not hide it, and state's pre-S1b grouped rows count as pred(args)."""
import runpy
from pathlib import Path

S = runpy.run_path(str(Path(__file__).resolve().parents[1] / "tools" / "s1-score.py"), run_name="s1score")


def test_padded_phrase_is_a_word_on_its_padded_side():
    a = S["norm"]("I cannot tell which crossing took d")
    assert not S["has"](" c ", a)
    assert S["has"](" c ", S["norm"]("d went with c, then c came back"))


def test_stems_still_match():
    assert S["has"]("carr", S["norm"]("the goat was carried across"))
    assert S["has"]("r2", S["norm"]("pick up r1 from top_r2"))


def test_quotes_around_a_name_do_not_hide_it():
    a = S["norm"](S["unquote"]("stacked as 'b' on 'c' on `a`; it can't move"))
    assert S["has"]("b on c", a) and S["has"]("can't", a)


def test_grouped_rows_count_as_atoms():
    outs = S["expand_rows"]("stage blocks\n  on        (b,c) (c,a)\n  clear     (b)")
    assert "on(c,a)" in outs and "clear(b)" in outs
