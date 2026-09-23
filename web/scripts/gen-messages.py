"""Mirrors the Go copy table into a TypeScript interface.

The interface this writes is what makes `t.NavChannels` a compile error when the
name is wrong, which is the same guarantee internal/admin/i18n/messages.go gives
on its side: a field that is missing does not build, rather than rendering as an
empty string that nobody notices until somebody opens that page in that
language.

Run it after changing the Go table:

    python web/scripts/gen-messages.py

It is a script rather than a `go:generate` step on purpose. This is the only
generator in the repository, it runs when a Go struct changes — which is rarely
— and wiring a Node or Python toolchain into `go build` for that would cost more
than it saves. The output is committed so a frontend checkout needs neither.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SRC = ROOT / "internal" / "admin" / "i18n" / "messages.go"
OUT = ROOT / "web" / "src" / "lib" / "messages.ts"

HEADER = """/**
 * The operator interface's copy, as the admin API serves it.
 *
 * GENERATED from internal/admin/i18n/messages.go by web/scripts/gen-messages.py.
 * Do not edit by hand: the next run overwrites it. Edit the Go table and
 * regenerate, which is also where the two translations live.
 *
 * Why mirror it at all, rather than fetch it loosely typed. A field the Go
 * table has and this does not is a TypeScript error at the line that uses it —
 * loud, immediate, and fixed by rerunning the generator. The alternative, an
 * index signature or a `Record<string, string>`, turns the same mistake into a
 * heading that renders as the word "undefined", on a page nobody opens until
 * they need it. That is the argument the Go package's own doc comment makes for
 * using a struct instead of a map, and it applies unchanged here.
 *
 * Every field is a string. The Go type is string or template.HTML, and the
 * difference between them is a rendering decision that only existed in
 * html/template — over JSON both arrive as text.
 */
export interface Messages {
  /** The subset app.js reads, served alongside the table. */
  Script: Script
"""

FOOTER = """}

/**
 * The confirms and flash messages, shared with the server-rendered pages.
 *
 * Named for the script that reads them there. Both interfaces show the same
 * sentences about the same actions, so they are one table and not two — a
 * delete confirmation that says one thing in the old interface and another in
 * the new one is the kind of difference that makes an operator distrust both.
 */
export interface Script {
{script_fields}
}

/**
 * The fields whose copy carries markup, generated from the Go type.
 *
 * {html_count} of the four hundred strings are typed template.HTML there, which
 * means the server-rendered pages interpolate them unescaped — the author wrote
 * <code> and <strong> into them and meant it. Over JSON they arrive as ordinary
 * strings, and React escapes an ordinary string, so without this the interface
 * renders the tags as visible text: a paragraph reading
 * "see <code>docs/07-api.md</code>", angle brackets and all.
 *
 * A generated set rather than a hand-written list, and rather than a flag in
 * the response. The list has to agree with the Go struct exactly, and the one
 * thing a generated list cannot do is drift from it — see the note at the top
 * of this file.
 *
 * Rendering these as markup is safe for the reason the Go templates already
 * rely on: every one of them is a sentence this repository's authors wrote, not
 * anything an operator or an API caller supplied.
 */
export const HTML_FIELDS: ReadonlySet<string> = new Set([
{html_fields}
])

/** A message field that carries markup. */
export type HTMLField =
{html_field_union}

/** One language the build ships. */
export interface LangOption {
  tag: string
  label: string
  on: boolean
}

/** What GET /admin/api/i18n answers with. */
export interface I18nPayload {
  lang: string
  langs: LangOption[]
  t: Messages
}
"""


def body_of(src: str, name: str) -> str | None:
    """Returns the inside of `type <name> struct { ... }`, or None."""
    match = re.search(rf"^type {name} struct \{{(.*?)^\}}", src, re.S | re.M)
    return match.group(1) if match else None


def fields(body: str) -> tuple[list[str], int, list[str]]:
    """Turns a Go struct body into TypeScript property lines.

    Also returns the names of the fields typed template.HTML, which the caller
    turns into a set the renderer reads — see the note on HTML_FIELDS.
    """
    lines: list[str] = []
    html_fields: list[str] = []
    count = 0

    for raw in body.split("\n"):
        text = raw.strip()

        # Section rules in the Go source become section comments here, so the
        # generated file reads in the same order as the one it mirrors.
        if text.startswith("// ----"):
            lines.append("")
            lines.append("  // " + text.strip("/ ").strip())
            continue

        field = re.match(
            r"^([A-Z][A-Za-z0-9]*)\s+(string|template\.HTML)\b\s*(?://\s?(.*))?$", text
        )
        if field:
            name, kind, comment = field.group(1), field.group(2), field.group(3)
            if comment:
                lines.append("  /** " + comment.replace("*/", "*\\/") + " */")
            lines.append(f"  {name}: string")
            count += 1
            if kind == "template.HTML":
                html_fields.append(name)

    return lines, count, html_fields


def main() -> int:
    src = SRC.read_text(encoding="utf-8")

    messages = body_of(src, "Messages")
    script = body_of(src, "Script")
    if messages is None or script is None:
        print(f"Messages or Script struct not found in {SRC}", file=sys.stderr)
        return 1

    # Messages.Script is declared by hand in the header; it is the one field
    # that is not a string, and skipping it here is what keeps the generated
    # interface honest about that rather than declaring it twice.
    lines, count, html_fields = fields(messages)
    script_lines, script_count, _ = fields(script)

    html_block = "\n".join(f"  '{name}'," for name in html_fields)
    html_union = "\n".join(f"  | '{name}'" for name in html_fields)
    footer = (
        FOOTER.replace("{script_fields}", "\n".join(script_lines))
        .replace("{html_fields}", html_block)
        .replace("{html_field_union}", html_union)
        .replace("{html_count}", str(len(html_fields)))
    )

    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(HEADER + "\n".join(lines) + "\n" + footer, encoding="utf-8")
    print(
        f"wrote {OUT.relative_to(ROOT)} with {count} messages, "
        f"{script_count} script strings and {len(html_fields)} markup fields"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
