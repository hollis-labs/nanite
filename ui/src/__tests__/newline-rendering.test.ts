/**
 * Regression test for CW-20260426-0005 — c39 newline rendering bug.
 *
 * Root cause: ReactMarkdown + remark-gfm only treats single `\n` as a
 * soft line break (collapses to a space per CommonMark spec). Agent output
 * commonly uses single newlines between lines, causing all content to
 * collapse into one line. Fix: add remark-breaks so single `\n` becomes
 * a `<br>` in the rendered output.
 *
 * This test validates the remark-breaks pipeline directly (no DOM needed):
 * running the unified processor on markdown with single newlines should
 * produce `<br />` nodes in the hast tree, not soft-wrap spaces.
 */

import rehypeStringify from "rehype-stringify";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import remarkRehype from "remark-rehype";
import { unified } from "unified";
import { describe, expect, it } from "vitest";

async function renderMarkdown(md: string): Promise<string> {
  const result = await unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkBreaks)
    .use(remarkRehype)
    .use(rehypeStringify)
    .process(md);
  return String(result);
}

async function renderMarkdownNoBreaks(md: string): Promise<string> {
  const result = await unified()
    .use(remarkParse)
    .use(remarkGfm)
    // remarkBreaks intentionally omitted — baseline without the fix
    .use(remarkRehype)
    .use(rehypeStringify)
    .process(md);
  return String(result);
}

describe("newline rendering (CW-20260426-0005 regression)", () => {
  it("single \\n produces <br> with remark-breaks", async () => {
    const input = "Line one\nLine two\nLine three";
    const html = await renderMarkdown(input);
    expect(html).toContain("<br>");
    // All three lines should be present as text nodes, not collapsed
    expect(html).toContain("Line one");
    expect(html).toContain("Line two");
    expect(html).toContain("Line three");
  });

  it("WITHOUT remark-breaks, single \\n collapses to space (demonstrating the bug)", async () => {
    const input = "Line one\nLine two\nLine three";
    const html = await renderMarkdownNoBreaks(input);
    // Without remark-breaks, the paragraph is a single text node with spaces
    expect(html).not.toContain("<br>");
  });

  it("double \\n\\n creates separate paragraphs with or without remark-breaks", async () => {
    const input = "Paragraph one\n\nParagraph two";
    const withBreaks = await renderMarkdown(input);
    const withoutBreaks = await renderMarkdownNoBreaks(input);
    // Both should produce two <p> tags
    expect(withBreaks.match(/<p>/g)?.length).toBe(2);
    expect(withoutBreaks.match(/<p>/g)?.length).toBe(2);
  });

  it("agent-style output with mixed newlines renders all lines", async () => {
    // Typical agent output: intro + single-newline list items
    const input = "Here is the result:\n\nStep 1: Install deps\nStep 2: Run tests\nStep 3: Deploy";
    const html = await renderMarkdown(input);
    expect(html).toContain("Here is the result:");
    expect(html).toContain("Step 1: Install deps");
    expect(html).toContain("Step 2: Run tests");
    expect(html).toContain("Step 3: Deploy");
    // Step lines should be separated by <br>, not collapsed
    expect(html).toContain("<br>");
  });
});
