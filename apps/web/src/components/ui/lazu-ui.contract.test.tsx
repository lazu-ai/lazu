import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { AuthProgress } from './auth-progress';
import { Button } from './button';
import { EmptyState } from './empty-state';
import { FieldMessage } from './field';
import { InlineNotice } from './inline-notice';
import { Input } from './input';
import { StatusDot } from './status-dot';
import { StatusPage } from './status-page';

describe('Lazu UI Brand contracts', () => {
  it('exposes loading state without relying on the spinner alone', () => {
    const html = renderToStaticMarkup(
      <Button loading loadingLabel='Saving'>
        Save
      </Button>,
    );
    expect(html).toContain('aria-busy="true"');
    expect(html).toContain('disabled=""');
    expect(html).toContain('Saving');
    expect(html).toContain('lazu-spin-icon');
  });

  it('keeps task completion feedback inside the stateful button', () => {
    const html = renderToStaticMarkup(
      <Button status='success' successLabel='Saved'>
        Save
      </Button>,
    );
    expect(html).toContain('data-status="success"');
    expect(html).toContain('aria-live="polite"');
    expect(html).toContain('Saved');
  });

  it('composes navigation as a link without nesting a button', () => {
    const html = renderToStaticMarkup(
      <Button
        render={<a aria-label='Open console' href='/console' />}
        variant='primary'
      >
        Open console
      </Button>,
    );
    expect(html).toContain('<a');
    expect(html).toContain('href="/console"');
    expect(html).toContain('lazu-button--primary');
    expect(html).not.toContain('<button');
  });

  it('preserves native invalid semantics for fields', () => {
    const html = renderToStaticMarkup(
      <Input aria-invalid='true' aria-describedby='name-error' />,
    );
    expect(html).toContain('aria-invalid="true"');
    expect(html).toContain('aria-describedby="name-error"');
  });

  it('does not expose an empty error alert before validation fails', () => {
    const html = renderToStaticMarkup(
      <FieldMessage error>{null}</FieldMessage>,
    );
    expect(html).toBe('');
  });

  it('keeps abnormal facts, impact and actions in one inline notice', () => {
    const html = renderToStaticMarkup(
      <InlineNotice
        actions={<Button>Recheck</Button>}
        description='Budget and member data remain available.'
        title='Channel telemetry is degraded'
        tone='warning'
      />,
    );
    expect(html).toContain('role="alert"');
    expect(html).toContain('lazu-inline-notice--warning');
    expect(html).toContain('Channel telemetry is degraded');
    expect(html).toContain('Budget and member data remain available.');
    expect(html).toContain('Recheck');
  });

  it('uses the locked circular-exclamation glyph for danger notices', () => {
    const html = renderToStaticMarkup(
      <InlineNotice title='Request failed' tone='danger' />,
    );
    expect(html).toContain('lucide-circle-alert');
    expect(html).not.toContain('lucide-circle-x');
  });

  it('emits tone classes used by non-color status shapes', () => {
    const warning = renderToStaticMarkup(
      <StatusDot tone='warning'>Degraded</StatusDot>,
    );
    const danger = renderToStaticMarkup(
      <StatusDot tone='danger'>Down</StatusDot>,
    );
    expect(warning).toContain('lazu-status-dot--warning');
    expect(warning).toContain('Degraded');
    expect(danger).toContain('lazu-status-dot--danger');
    expect(danger).toContain('Down');
  });

  it('renders the continuous authentication sequence and current step', () => {
    const html = renderToStaticMarkup(<AuthProgress current={1} />);
    expect(html).toContain('Identity');
    expect(html).toContain('Workspace');
    expect(html).toContain('Complete');
    expect(html).toContain('aria-current="step"');
    expect(html).toContain('is-complete');
  });

  it('states the failure, its context and the way out on one page', () => {
    const html = renderToStaticMarkup(
      <StatusPage
        action={<Button>Go to console</Button>}
        description='This address does not match a Lazu page.'
        hint='Did you mean Models?'
        mark='404'
        pill='GET /console/modles'
        title='Page not found'
      />,
    );
    expect(html).toContain('Page not found');
    expect(html).toContain('GET /console/modles');
    expect(html).toContain('Go to console');
    expect(html).toContain('Did you mean Models?');
    // An HTTP code is set upright; the mark is decorative to assistive tech
    // because the title already says the same thing in words.
    expect(html).toContain('aria-hidden="true" class="lazu-status-page__mark"');
    expect(html).not.toContain('role="alert"');
  });

  it('sets a word mark in italic and leaves out empty slots', () => {
    const html = renderToStaticMarkup(
      <StatusPage
        description='Refreshing usually fixes it.'
        live
        mark='Error'
        title='This page hit an error'
      />,
    );
    expect(html).toContain('lazu-status-page__mark--word');
    expect(html).toContain('role="alert"');
    expect(html).not.toContain('lazu-status-page__pill');
    expect(html).not.toContain('lazu-status-page__actions');
    expect(html).not.toContain('lazu-status-page__hint');
  });

  it('only renders an empty slot when it contains a first action', () => {
    const actionable = renderToStaticMarkup(
      <EmptyState action={<Button>Create key</Button>} title='No keys yet' />,
    );
    const readOnly = renderToStaticMarkup(
      <EmptyState title='No results in this time range' />,
    );
    expect(actionable).toContain('lazu-empty__slot');
    expect(actionable).toContain('Create key');
    expect(readOnly).toContain('lazu-empty__plain');
    expect(readOnly).not.toContain('lazu-empty__slot');
  });
});
