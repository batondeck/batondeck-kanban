/**
 * A ```mermaid fence that does not parse falls back to its source, and leaves nothing behind.
 *
 * Mermaid renders into a temporary element it appends to <body>, and on a parse error it drew its
 * own "Syntax error in text" graphic there and threw before removing it. The block fell back
 * correctly, but the graphic stayed at the bottom of the document, outside React's root, across
 * navigation, one more for each mount of such a block. MERMAID IS THE REAL LIBRARY here.
 */
import { describe, it, expect, beforeAll, afterEach } from 'vitest';
import { render, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { ChronicleBlockView } from '../chronicleBlocks.js';
import type { ChronicleBlock } from '../types.js';

beforeAll(() => {
  const svg = window.SVGElement.prototype as unknown as Record<string, unknown>;
  svg.getBBox = () => ({ x: 0, y: 0, width: 80, height: 16 });
  svg.getComputedTextLength = () => 80;
});

afterEach(cleanup);

const block = (text: string): ChronicleBlock =>
  ({ id: 'b1', state: 'authored', text, citations: [], origin: null }) as unknown as ChronicleBlock;

describe('a mermaid fence that fails to parse', () => {
  it('falls back to its source and leaves no error graphic in <body>', async () => {
    const view = render(
      <MemoryRouter>
        <ChronicleBlockView block={block('```mermaid\ngraph TD; A-->>>B;;; ((((\n```')} />
      </MemoryRouter>,
    );
    await waitFor(() => expect(document.querySelector('pre.chr-mermaid-src')).toBeTruthy(), { timeout: 10_000 });
    const strays = [...document.body.children].filter((el) => el !== view.container);
    expect(strays.map((el) => `${el.tagName}#${el.id}`), 'mermaid left its temporary element behind').toEqual([]);
    expect(document.body.textContent).not.toMatch(/Syntax error/i);
  });
});
