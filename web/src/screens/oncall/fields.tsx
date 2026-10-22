/**
 * Form primitives for the on-call setup drawer.
 *
 * These exist because the integration config has ~20 settings across seven groups, and writing each
 * one inline produced a 400-line JSX blob where the field and its explanation drifted apart. Each
 * control here carries its own label and hint, so a setting and the sentence saying what it does
 * cannot be separated by an edit.
 *
 * `MapEditor` is the one worth naming: FOUR different settings are string→string maps
 * (`severityMap`, `statusMap`, `pdUserMap`, and the custom `defaults`), and every one of them was
 * previously unreachable from the UI — the drawer told operators to go and use MCP instead.
 */
import { useState, type ReactNode } from 'react';
import { Icon } from '../../icons.js';
import { Cluster, Note, Stack } from '../../ui.js';

export function Field({
  label,
  hint,
  htmlFor,
  required,
  children,
}: {
  label: string;
  hint?: ReactNode;
  htmlFor?: string;
  /** Renders a marker beside the label. Carries `aria-hidden` because the input itself is what an
   *  assistive technology should hear "required" from, and duplicating it reads the word twice. */
  required?: boolean;
  children: ReactNode;
}) {
  return (
    <Stack gap={4}>
      {/*
        THE MARKER SITS OUTSIDE `<label>`, and that placement is the whole point.

        Inside it, the label's text content becomes "Name required" — so the input's ACCESSIBLE NAME
        changes, and every `getByLabelText(/^Name$/)` in the suite stops matching. `aria-hidden` does
        not save it: name computation from a label element is text-content based here, and jsdom's
        matcher ignores the attribute. Caught by `oncall-manage.test.tsx` failing the moment the
        first field was marked required.

        "(required)" / "(optional)" used to be typed into the label STRING, which styled them
        identically to the field name and was applied inconsistently field to field.
      */}
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 6 }}>
        <label htmlFor={htmlFor}>{label}</label>
        {required && (
          <span
            aria-hidden="true"
            style={{
              fontFamily: 'var(--mono)',
              fontSize: 10,
              letterSpacing: '0.04em',
              textTransform: 'uppercase',
              color: 'var(--text-muted)',
            }}
          >
            required
          </span>
        )}
      </div>
      {children}
      {hint && <Note>{hint}</Note>}
    </Stack>
  );
}

/**
 * A titled run of fields inside a Panel.
 *
 * The custom-webhook mapping panel was ELEVEN fields in one flat column, each disambiguated only by
 * an "(optional)" suffix the reader had to notice. Reported from staging as *"the form fields lack
 * clarity"*. Grouping is the fix that costs nothing at runtime: one required field, then the fields
 * that decide what the ticket SAYS, then the ones most integrations never touch.
 */
export function FieldGroup({
  title,
  hint,
  children,
}: {
  title: string;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Stack gap={8}>
      <Stack gap={2}>
        <span
          style={{
            fontFamily: 'var(--mono)',
            fontSize: 10,
            letterSpacing: '0.06em',
            textTransform: 'uppercase',
            color: 'var(--text-muted)',
          }}
        >
          {title}
        </span>
        {hint && <span className="muted" style={{ fontSize: 12 }}>{hint}</span>}
      </Stack>
      <div style={{ borderLeft: '2px solid var(--rule)', paddingLeft: 12 }}>
        <Stack gap={12}>{children}</Stack>
      </div>
    </Stack>
  );
}

export function TextField({
  id,
  label,
  hint,
  value,
  onChange,
  placeholder,
  required,
  type = 'text',
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  required?: boolean;
  type?: 'text' | 'number' | 'password';
}) {
  return (
    <Field label={label} hint={hint} htmlFor={id} required={required}>
      <input
        id={id}
        className="input"
        type={type}
        value={value}
        placeholder={placeholder}
        required={required}
        onChange={(e) => onChange(e.target.value)}
      />
    </Field>
  );
}

export function Toggle({
  id,
  label,
  hint,
  checked,
  onChange,
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <Stack gap={4}>
      <Cluster gap={8}>
        <input
          id={id}
          type="checkbox"
          checked={checked}
          onChange={(e) => onChange(e.target.checked)}
        />
        <label htmlFor={id}>{label}</label>
      </Cluster>
      {hint && <Note>{hint}</Note>}
    </Stack>
  );
}

export function RadioRow<T extends string>({
  name,
  label,
  hint,
  options,
  value,
  onChange,
}: {
  name: string;
  label: string;
  hint?: ReactNode;
  options: Array<{ value: T; label: string; note?: string }>;
  value: T;
  onChange: (v: T) => void;
}) {
  return (
    <Stack gap={4}>
      <span>{label}</span>
      <Stack gap={4}>
        {options.map((o) => (
          <Cluster key={o.value} gap={8}>
            <input
              id={`${name}-${o.value}`}
              type="radio"
              name={name}
              checked={value === o.value}
              onChange={() => onChange(o.value)}
            />
            <label htmlFor={`${name}-${o.value}`}>
              {o.label}
              {o.note ? <span className="muted"> — {o.note}</span> : null}
            </label>
          </Cluster>
        ))}
      </Stack>
      {hint && <Note>{hint}</Note>}
    </Stack>
  );
}

/**
 * A string→string map, edited as rows. Values are constrained to `allowed` when it is given, which
 * is what makes `severityMap` (→ priority) and `statusMap` (→ firing/resolved/test) safe to type
 * freely on the key side while the value side cannot be wrong.
 */
export function MapEditor({
  id,
  label,
  hint,
  value,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
  allowed,
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  value: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
  allowed?: readonly string[];
}) {
  const [draftKey, setDraftKey] = useState('');
  const [draftValue, setDraftValue] = useState(allowed?.[0] ?? '');
  const entries = Object.entries(value);
  const add = (): void => {
    const k = draftKey.trim();
    if (!k || !draftValue) return;
    onChange({ ...value, [k]: draftValue });
    setDraftKey('');
    setDraftValue(allowed?.[0] ?? '');
  };
  return (
    <Field label={label} hint={hint}>
      <Stack gap={4}>
        {entries.map(([k, v]) => (
          <Cluster key={k} gap={8} wrap>
            <code>{k}</code>
            <span aria-hidden="true">→</span>
            <code>{v}</code>
            <button
              className="btn secondary"
              type="button"
              aria-label={`Remove ${k}`}
              onClick={() => {
                const next = { ...value };
                delete next[k];
                onChange(next);
              }}
            >
              <Icon name="close" size={12} />
            </button>
          </Cluster>
        ))}
        <Cluster gap={8} wrap>
          <input
            id={`${id}-key`}
            className="input"
            style={{ maxWidth: 180 }}
            aria-label={`${label} key`}
            value={draftKey}
            placeholder={keyPlaceholder}
            onChange={(e) => setDraftKey(e.target.value)}
          />
          {allowed ? (
            <select
              id={`${id}-value`}
              className="input"
              style={{ maxWidth: 160 }}
              aria-label={`${label} value`}
              value={draftValue}
              onChange={(e) => setDraftValue(e.target.value)}
            >
              {allowed.map((a) => (
                <option key={a} value={a}>
                  {a}
                </option>
              ))}
            </select>
          ) : (
            <input
              id={`${id}-value`}
              className="input"
              style={{ maxWidth: 180 }}
              aria-label={`${label} value`}
              value={draftValue}
              placeholder={valuePlaceholder}
              onChange={(e) => setDraftValue(e.target.value)}
            />
          )}
          <button className="btn secondary" type="button" onClick={add}>
            Add
          </button>
        </Cluster>
      </Stack>
    </Field>
  );
}

/**
 * A FALLBACK CHAIN of paths, entered as one comma-separated line.
 *
 * Deliberately not a repeating row editor: a chain is read left-to-right ("this, or this, or this")
 * and one line reads that way while a stack of boxes does not. Empty segments are dropped, so a
 * trailing comma while typing is not an error.
 */
export function ChainField({
  id,
  label,
  hint,
  value,
  onChange,
  placeholder,
  required,
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  value: string[] | undefined;
  onChange: (v: string[] | undefined) => void;
  placeholder?: string;
  required?: boolean;
}) {
  return (
    <TextField
      id={id}
      label={label}
      hint={hint}
      required={required}
      value={(value ?? []).join(', ')}
      placeholder={placeholder}
      onChange={(raw) => {
        const parts = raw
          .split(',')
          .map((p) => p.trim())
          .filter(Boolean);
        onChange(parts.length > 0 ? parts : undefined);
      }}
    />
  );
}

/** A comma-separated list of plain strings (labels, annotation allowlist). */
export function ListField(props: {
  id: string;
  label: string;
  hint?: ReactNode;
  value: string[] | undefined;
  onChange: (v: string[] | undefined) => void;
  placeholder?: string;
}) {
  return <ChainField {...props} />;
}
