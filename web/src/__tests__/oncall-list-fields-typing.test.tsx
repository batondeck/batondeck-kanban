/**
 * THE COMMA-SEPARATED FIELDS CAN BE TYPED, not only pasted.
 *
 * `ChainField` (a fallback chain of paths) and `ListField` (labels, the annotation allowlist) used to
 * render `value.join(', ')` from the PARSED array on every keystroke. A trailing `,` or ` ` parses to
 * nothing, so it never reached state, and the controlled input was written back without it: typing
 * `alert.id, $body_hash` (the form's own placeholder) left `alert.id$body_hash`, one path. The older
 * tests set the whole string in one change event, which is a paste, so they passed.
 *
 * `type` below is what a browser does: one change event per character, each carrying the input's
 * CURRENT value plus that character.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { useState } from 'react';
import { fireEvent, render, screen, cleanup } from '@testing-library/react';
import { ChainField, ListField } from '../screens/oncall/fields.js';

afterEach(cleanup);

const type = (input: HTMLInputElement, text: string): void => {
  for (const ch of text) fireEvent.change(input, { target: { value: input.value + ch } });
};

/** The field under a parent that holds the parsed value, as IntegrationForm does. */
function Harness({
  kind,
  initial,
  onValue,
}: {
  kind: 'chain' | 'list';
  initial?: string[];
  onValue: (v: string[] | undefined) => void;
}) {
  const [value, setValue] = useState<string[] | undefined>(initial);
  const Field = kind === 'chain' ? ChainField : ListField;
  return (
    <>
      <Field
        id="f"
        label="Paths"
        value={value}
        onChange={(v) => {
          setValue(v);
          onValue(v);
        }}
      />
      {/* What re-seeding looks like from the field's side: the parent replaces the value. */}
      <button type="button" onClick={() => setValue(['other.path'])}>
        Reseed
      </button>
    </>
  );
}

describe('typing into a comma-separated field', () => {
  it('a fallback chain typed one key at a time keeps its comma and space', () => {
    let last: string[] | undefined;
    render(<Harness kind="chain" onValue={(v) => (last = v)} />);
    const input = screen.getByLabelText('Paths') as HTMLInputElement;
    type(input, 'alert.id, $body_hash');
    expect(input.value).toBe('alert.id, $body_hash');
    expect(last).toEqual(['alert.id', '$body_hash']);
  });

  it('a two-label list typed one key at a time is two labels', () => {
    let last: string[] | undefined;
    render(<Harness kind="list" onValue={(v) => (last = v)} />);
    const input = screen.getByLabelText('Paths') as HTMLInputElement;
    type(input, 'oncall, prod');
    expect(input.value).toBe('oncall, prod');
    expect(last).toEqual(['oncall', 'prod']);
  });

  it('opens on the configured value and follows a value replaced from outside', () => {
    render(<Harness kind="chain" initial={['a', 'b']} onValue={() => {}} />);
    const input = screen.getByLabelText('Paths') as HTMLInputElement;
    expect(input.value).toBe('a, b');
    type(input, ',');
    expect(input.value, 'a trailing comma stays while typing').toBe('a, b,');
    fireEvent.click(screen.getByRole('button', { name: 'Reseed' }));
    expect(input.value, 'a new value from the parent replaces the draft').toBe('other.path');
  });

  it('clearing the field clears the value', () => {
    let last: string[] | undefined = ['x'];
    render(<Harness kind="list" initial={['x']} onValue={(v) => (last = v)} />);
    const input = screen.getByLabelText('Paths') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '' } });
    expect(input.value).toBe('');
    expect(last).toBeUndefined();
  });
});
