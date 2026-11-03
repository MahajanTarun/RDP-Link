"""Tests for the setup assistant's checks and fixes."""

import shlex

from rdp_manager import setupcheck


def test_mac_firewall_script_quotes_paths_with_spaces():
    agent = "/Volumes/Tarun's SSD/Apps/RDP Link.app/Contents/MacOS/office-rdp"
    script = setupcheck._mac_firewall_script(agent)
    # The path survives the AppleScript string and the shell.
    inner = script[len('do shell script "'):script.rindex('" with administrator')]
    inner = inner.replace('\\"', '"')
    parts = shlex.split(inner)
    assert agent in parts, inner
    assert "&&" in parts


def test_mac_firewall_script_escapes_quotes():
    agent = '/tmp/weird "name"/office-rdp'
    script = setupcheck._mac_firewall_script(agent)
    assert '\\"' in script
    inner = script[len('do shell script "'):script.rindex('" with administrator')]
    inner = inner.replace('\\"', '"')
    parts = shlex.split(inner)
    assert agent in parts


def test_summarize_counts_problems():
    items = [
        {"status": "ok"}, {"status": "warn"}, {"status": "fail"}, {"status": "info"},
    ]
    bad, total = setupcheck.summarize(items)
    assert (bad, total) == (2, 4)


def test_collect_returns_items():
    items = setupcheck.collect()
    assert items
    for item in items:
        assert item["status"] in ("ok", "warn", "fail", "info")
        assert item["label"]
