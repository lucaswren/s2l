#!/usr/bin/env python3
"""Check local Markdown links, heading fragments, screenshots and language tags."""

from pathlib import Path
import re
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]


def markdown_anchors(text):
    """Heading IDs for the ATX headings used by the repository's GitHub docs."""
    anchors = set()
    fence = None
    for line in text.splitlines():
        marker = re.match(r"^\s{0,3}(`{3,}|~{3,})", line)
        if marker:
            run = marker[1]
            if fence is None:
                fence = run
            elif run[0] == fence[0] and len(run) >= len(fence):
                fence = None
            continue
        heading = re.match(r"^#{1,6}\s+(.+?)\s*#*\s*$", line)
        if fence is not None or not heading:
            continue
        slug = re.sub(r"[^\w\- ]", "", heading[1].lower()).replace(" ", "-")
        anchor = slug
        suffix = 0
        while anchor in anchors:
            suffix += 1
            anchor = f"{slug}-{suffix}"
        anchors.add(anchor)
    return anchors


def main():
    documents = sorted(ROOT.glob("*.md")) + sorted((ROOT / "docs").rglob("*.md"))
    errors = []
    links = 0
    anchors = {}
    for path in documents:
        text = path.read_text(encoding="utf-8")
        name = path.relative_to(ROOT).as_posix()
        if path.name.startswith("README") or ROOT / "docs" in path.parents:
            lines = text.splitlines()
            labels = re.findall(r"\[([^\]]+)\]\(", lines[2] if len(lines) > 2 else "")
            if labels[-3:] != ["English", "繁體中文", "简体中文"]:
                errors.append(f"{name}: expected fixed navigation labels English, 繁體中文, 简体中文")
        if re.search(r"zh-(?:TW|CN)\b", text):
            errors.append(f"{name}: use script-based Chinese language tags")
        language = next((lang for lang in ('en', 'zh-Hant', 'zh-Hans') if lang in path.parts or path.name == f'README.{lang}.md'), 'en')
        if path.name in ('README.md', 'README.zh-Hant.md', 'README.zh-Hans.md'):
            folder = 'docs/assets/' + (f'{language}/' if language != 'en' else '')
            for view in ('overview', 'mapping', 'evidence'):
                if f']({folder}showcase-{view}.png)' not in text:
                    errors.append(f"{name}: expected matching {language} {view} screenshot")
        for line in text.splitlines():
            if (line.startswith('curl ') and '/script/remote_install.sh ' in line) or (line.startswith('sudo ') and 'bash script/install.sh' in line):
                if f'--lang {language}' not in line:
                    errors.append(f"{name}: installation example must select --lang {language}")
        # Current documents use inline links, without titles or escaped parentheses.
        for target in re.findall(r"\]\(([^)]+)\)", text):
            target = target.strip().strip("<>")
            parsed = urlsplit(target)
            if parsed.scheme or parsed.netloc:
                continue
            links += 1
            resolved = (path.parent / unquote(parsed.path)).resolve() if parsed.path else path
            if not resolved.exists():
                errors.append(f"{name}: missing link target {target}")
            elif parsed.fragment and resolved.suffix == '.md':
                if resolved not in anchors:
                    anchors[resolved] = markdown_anchors(resolved.read_text(encoding='utf-8'))
                if unquote(parsed.fragment) not in anchors[resolved]:
                    errors.append(f"{name}: missing heading in link target {target}")
    html = (ROOT / "web" / "index.html").read_text(encoding="utf-8")
    if '<html lang="zh-Hans">' not in html:
        errors.append("web/index.html: expected zh-Hans for the Simplified Chinese UI")
    if errors:
        raise SystemExit("\n".join(errors))
    print(f"Checked {links} local links and heading fragments in {len(documents)} Markdown files, README screenshot languages and UI language tag")


if __name__ == "__main__":
    main()
