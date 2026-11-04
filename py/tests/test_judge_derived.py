"""The judge on a bundle with generated units (loop-prompt s-3591d53b): a pack names the source; the copy is remade."""
import pytest

pytest.importorskip("metis.trace")


def test_a_source_s_generated_units_are_remade_in_the_copy(tmp_path):
    """loop-prompt (the architect): a pack's unit= names the source, never the generated file; the judge remakes the
    units generated from it (% DERIVED from SRC, alfworld's tree/) in its copy, by the Makefile above them."""
    from metis_agent.trace import _remake_derived
    from metis import trace as T
    hier = tmp_path / "domains" / "hier"
    (hier / "tree").mkdir(parents=True)
    (hier / "nav_domain.llp").write_text("go [dyn] : goto(R) -o reached(R).\n")
    gen = "% DERIVED from ../nav_domain.llp by gen (make): edit that file, never this one.\n"
    (hier / "tree" / "nav_domain.llp").write_text(gen + "stale\n")
    (hier / "tree" / "other.llp").write_text("% DERIVED from ../other_domain.llp by gen (make)\n")
    (hier / "hdr").write_text(gen)
    (hier / "Makefile").write_text("tree: nav_domain.llp\n\tcat hdr nav_domain.llp | sed 's/reached(R)\\./reached(R) * done./' > tree/nav_domain.llp\n")
    (hier / "nav_domain.llp").write_text("go [dyn] : goto(R) * $here -o reached(R).\n")      # the pack, written into the source
    assert _remake_derived(tmp_path, hier / "nav_domain.llp") == ["domains/hier/tree"]
    assert (hier / "tree" / "nav_domain.llp").read_text() == gen + "go [dyn] : goto(R) * $here -o reached(R) * done.\n"
    assert _remake_derived(tmp_path, hier / "missing.llp") == []                        # nothing generated from it
    (hier / "Makefile").write_text("tree:\n\t@echo the generator broke >&2; exit 3\n")
    with pytest.raises(T.TraceError, match="make tree in domains/hier failed .*the generator broke"):
        _remake_derived(tmp_path, hier / "nav_domain.llp")
    (hier / "Makefile").unlink()
    with pytest.raises(T.TraceError, match="no Makefile above it"):
        _remake_derived(tmp_path, hier / "nav_domain.llp")
