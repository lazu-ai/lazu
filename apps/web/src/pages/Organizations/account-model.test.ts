import { describe, expect, it } from 'vitest';
import {
  accountKeyOperationalState,
  adjacentAccountTarget,
  countOperationalKeys,
  daysToEmpty,
  isDeleted,
  toAccount,
} from './account-model';

describe('customer account key state', () => {
  it('gives account suspension precedence over an enabled key', () => {
    expect(
      accountKeyOperationalState(
        { status: 2 },
        { status: 1, expired_time: 0 },
        1_800_000_000,
      ),
    ).toBe('account_disabled');
  });

  it('keeps expiry ahead of the persisted enabled state', () => {
    expect(
      accountKeyOperationalState(
        { status: 1 },
        { status: 1, expired_time: 1_700_000_000 },
        1_800_000_000,
      ),
    ).toBe('expired');
  });

  it('counts only live operational keys for the customer card strip', () => {
    const keys = [
      { status: 1, expired_time: 0 },
      { status: 2, expired_time: 0 },
      { status: 1, expired_time: 1_700_000_000 },
    ];
    expect(countOperationalKeys({ status: 1 }, keys, 1_800_000_000)).toBe(1);
    expect(countOperationalKeys({ status: 2 }, keys, 1_800_000_000)).toBe(0);
  });
});

describe('customer account runway estimate', () => {
  it('computes days-to-empty from recent spend window', () => {
    const days = daysToEmpty({
      id: 'usr_1',
      name: 'Grace',
      balance_microusd: 15_000_000,
      recent_spend_microusd: 30_000_000,
      recent_spend_days: 30,
    });
    expect(days).toBe(15);
  });
});

describe('customer account adjacent navigation', () => {
  const rows = [
    { id: 'acct-21', name: 'Twenty one' },
    { id: 'acct-22', name: 'Twenty two' },
  ];

  it('returns the adjacent row while it is loaded', () => {
    expect(adjacentAccountTarget(rows, 'acct-21', 2, 20, 43, 'next')).toEqual({
      account: rows[1],
      kind: 'row',
    });
  });

  it('continues next navigation on the following page', () => {
    expect(adjacentAccountTarget(rows, 'acct-22', 2, 20, 43, 'next')).toEqual({
      kind: 'page',
      page: 3,
      pick: 'first',
    });
  });

  it('continues previous navigation on the preceding page', () => {
    expect(
      adjacentAccountTarget(rows, 'acct-21', 2, 20, 43, 'previous'),
    ).toEqual({ kind: 'page', page: 1, pick: 'last' });
  });

  it('stops only at the true first and last customers', () => {
    expect(adjacentAccountTarget(rows, 'acct-21', 1, 20, 2, 'previous')).toBe(
      null,
    );
    expect(adjacentAccountTarget(rows, 'acct-22', 1, 20, 2, 'next')).toBe(null);
  });
});

describe('deleted accounts in the list', () => {
  it('reads the deletion time and restore deadline from a list row', () => {
    const account = toAccount({
      id: 'usr_1',
      username: 'gone',
      DeletedAt: '2026-11-01T08:00:00Z',
      restore_until: 1_794_384_000,
    });
    expect(account.deleted_at).toBe(Date.parse('2026-11-01T08:00:00Z') / 1000);
    expect(account.restore_until).toBe(1_794_384_000);
    expect(isDeleted(account)).toBe(true);
  });

  it('treats a null DeletedAt as a live account', () => {
    const account = toAccount({
      id: 'usr_2',
      username: 'here',
      DeletedAt: null,
    });
    expect(account.deleted_at).toBeUndefined();
    expect(isDeleted(account)).toBe(false);
  });
});
