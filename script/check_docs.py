#!/usr/bin/env python3
"""Check local inline Markdown links and language tags in repository documents."""

from pathlib import Path
import re
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]


def main():
    documents = sorted(ROOT.glob("*.md")) + sorted((ROOT / "docs").rglob("*.md"))
    errors = []
    links = 0
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
        language = next((lang for lang in ('en', 'zh-Hant', 'zh-Hans') if lang in path.parts or lang in path.name), 'en')
        for line in text.splitlines():
            if (line.startswith('curl ') and '/script/remote_install.sh ' in line) or (line.startswith('sudo ') and 'bash script/install.sh' in line):
                if f'--lang {language}' not in line:
                    errors.append(f"{name}: installation example must select --lang {language}")
        # Current documents use inline links, without titles or escaped parentheses.
        for target in re.findall(r"\]\(([^)]+)\)", text):
            target = target.strip().strip("<>")
            parsed = urlsplit(target)
            if parsed.scheme or parsed.netloc or not parsed.path:
                continue
            links += 1
            resolved = path.parent / unquote(parsed.path)
            if not resolved.exists():
                errors.append(f"{name}: missing link target {target}")
    html = (ROOT / "web" / "index.html").read_text(encoding="utf-8")
    if '<html lang="zh-Hans">' not in html:
        errors.append("web/index.html: expected zh-Hans for the Simplified Chinese UI")
    if errors:
        raise SystemExit("\n".join(errors))
    print(f"Checked {links} local links in {len(documents)} Markdown files and UI language tag")


if __name__ == "__main__":
    main()
