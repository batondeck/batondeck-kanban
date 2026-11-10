/**
 * The connect dialog's plugin snippet reaches THIS server.
 *
 * The plugin's .mcp.json reads `${BATONDECK_URL:-http://localhost:8080}/mcp`, and the dialog told a
 * reader on any other address that it "reads http://localhost:8080/mcp", with no word of the
 * variable: the copied command started a plugin session that could not reach the board.
 */
import { describe, it, expect } from 'vitest';
import { snippets } from '../connect-mcp.js';

const plugin = (mcpUrl: string) => {
  const s = snippets(mcpUrl)['Claude Code'];
  return { text: s.blocks.find((b) => b.text.includes('--plugin-dir'))!.text, note: s.note };
};

describe('the plugin snippet', () => {
  it('on another address, exports BATONDECK_URL to this server’s origin first', () => {
    const { text, note } = plugin('http://10.0.0.5:9090/mcp');
    expect(text.split('\n')[0]).toBe("export BATONDECK_URL='http://10.0.0.5:9090'");
    expect(text).toContain('claude --plugin-dir');
    expect(note).toContain('BATONDECK_URL');
    expect(note).not.toMatch(/reads http:\/\/localhost:8080\/mcp/);
  });

  it('on the default address, needs no export', () => {
    const { text, note } = plugin('http://localhost:8080/mcp');
    expect(text).not.toContain('BATONDECK_URL');
    expect(note).toContain('BATONDECK_URL');
  });
});
