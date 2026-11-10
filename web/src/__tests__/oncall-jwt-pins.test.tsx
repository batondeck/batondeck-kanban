/**
 * A JWT INTEGRATION CAN PIN WHO THE TOKEN NAMES.
 *
 * On a shared issuer (Google, GitHub Actions) anyone with an account there can mint a correctly
 * signed token for an audience they choose, so issuer + audience alone let every one of them post
 * alerts. The form offers the core's two optional pins, the token's subject and its verified email,
 * says under Issuer why they matter, and sends them only when filled in.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { useState } from 'react';
import { fireEvent, render, screen, cleanup } from '@testing-library/react';
import { IntegrationForm, blankForm, createBodyOf, type FormState } from '../screens/oncall/IntegrationForm.js';
import type { OncallSource } from '../types.js';

const WEBHOOK: OncallSource = {
  id: 'webhook',
  label: 'Custom webhook',
  authModes: ['hmac', 'api_key', 'basic', 'jwt'],
  docsPage: 'oncall-webhook',
  authNote: 'Any of the four modes.',
};

afterEach(cleanup);

let latest: FormState;
function Harness({ isCreate = true }: { isCreate?: boolean }) {
  const [form, setForm] = useState<FormState>({ ...blankForm(WEBHOOK), authMode: 'jwt' });
  latest = form;
  return (
    <IntegrationForm
      source={WEBHOOK}
      form={form}
      set={(p) => setForm((f) => ({ ...f, ...p }))}
      isCreate={isCreate}
      onGenerateKey={() => {}}
    />
  );
}

describe('the jwt auth fields', () => {
  it('offer a subject and an email pin, and say issuer and audience alone do not identify the sender', () => {
    render(<Harness />);
    expect(screen.getByText(/issuer and audience alone do not identify the sender/i)).toBeTruthy();
    fireEvent.change(screen.getByLabelText(/Subject pin/i), { target: { value: ' 1170 ' } });
    fireEvent.change(screen.getByLabelText(/Email pin/i), { target: { value: 'monitoring@example.iam.gserviceaccount.com' } });
    fireEvent.change(screen.getByLabelText(/^Issuer/i), { target: { value: 'https://accounts.google.com' } });
    fireEvent.change(screen.getByLabelText(/^Audience/i), { target: { value: 'batondeck' } });
    fireEvent.change(screen.getByLabelText(/JWKS URL/i), { target: { value: 'https://www.googleapis.com/oauth2/v3/certs' } });
    expect(createBodyOf(latest, WEBHOOK).auth).toEqual({
      mode: 'jwt',
      jwt: {
        issuer: 'https://accounts.google.com',
        audience: 'batondeck',
        jwksUrl: 'https://www.googleapis.com/oauth2/v3/certs',
        subject: '1170',
        email: 'monitoring@example.iam.gserviceaccount.com',
      },
    });
  });

  // The stored jwt block is never shown back, so an edit re-enters it whole and replaces it: a pin
  // left blank there removes the stored one, and the form says so before the save.
  it('say on an edit that the settings are replaced whole and a blank pin removes the stored one', () => {
    render(<Harness isCreate={false} />);
    expect(screen.getByText(/a blank pin removes the one stored/i)).toBeTruthy();
    cleanup();
    render(<Harness />);
    expect(screen.queryByText(/a blank pin removes the one stored/i), 'a new integration has nothing stored').toBeNull();
  });

  it('send no pin that was left blank (control)', () => {
    const f: FormState = {
      ...blankForm(WEBHOOK),
      authMode: 'jwt',
      jwtIssuer: 'https://idp.example.com/',
      jwtAudience: 'batondeck',
      jwtJwksUrl: 'https://idp.example.com/jwks',
    };
    expect(createBodyOf(f, WEBHOOK).auth).toEqual({
      mode: 'jwt',
      jwt: { issuer: 'https://idp.example.com/', audience: 'batondeck', jwksUrl: 'https://idp.example.com/jwks' },
    });
  });
});
