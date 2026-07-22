import { expect, test, type Page } from '@playwright/test';

import { mockApi, RECS, seedAuth, type MockHandle, type SmokeRec } from './fixtures/recs';

async function openOpportunities(page: Page): Promise<void> {
  await seedAuth(page);
  await page.goto('/');
  await page.getByRole('tab', { name: 'Opportunities' }).click();
  await expect(page.locator('.recommendations-filter-live')).toContainText(
    `Showing ${RECS.length} of ${RECS.length} recommendations`,
  );
}

async function selectRecommendation(page: Page, id: string): Promise<void> {
  const row = page.locator(`tr.recommendation-row[data-rec-id="${id}"]`);
  await row.getByRole('checkbox', { name: 'Select recommendation' }).check();
}

function callsTo(handle: MockHandle, suffix: string): Array<Record<string, unknown>> {
  return handle.calls
    .filter((call) => call.method === 'POST' && new URL(call.url).pathname === suffix)
    .map((call) => JSON.parse(call.postData ?? '{}') as Record<string, unknown>);
}

test('filters rows by a numeric expression derived from the fixture', async ({ page }) => {
  await mockApi(page);
  await openOpportunities(page);

  const expected = RECS.filter((rec) => rec.savings > 100);
  await page.getByRole('button', { name: 'Filter Monthly Savings' }).click();
  const expression = page.getByRole('dialog', { name: 'Filter Monthly Savings' })
    .getByLabel('Expression');
  await expression.fill('>100');
  await expression.press('Enter');

  await expect(page.locator('tr.recommendation-row')).toHaveCount(expected.length);
  await expect(page.locator('.recommendations-filter-live')).toHaveText(
    `Showing ${expected.length} of ${RECS.length} recommendations`,
  );
});

test('shows loading and zero-result filter states without losing filter controls', async ({ page }) => {
  const api = await mockApi(page);
  await openOpportunities(page);
  await page.getByRole('tab', { name: 'Home' }).click();
  api.delayNextRecommendationsFetch(3_000);

  await Promise.all([
    expect(page.locator('#recommendations-list .skeleton-row')).toHaveCount(5),
    page.getByRole('tab', { name: 'Opportunities' }).click(),
  ]);
  await expect(page.locator('.recommendations-filter-live')).toContainText(`Showing ${RECS.length}`);

  await page.getByRole('button', { name: 'Filter Monthly Savings' }).click();
  const expression = page.getByRole('dialog', { name: 'Filter Monthly Savings' })
    .getByLabel('Expression');
  await expression.fill('>999999');
  await expression.press('Enter');

  await expect(page.locator('#recommendations-list thead')).toBeVisible();
  await expect(page.locator('#recommendations-list tbody .empty')).toHaveText(
    'No rows match these filters.',
  );
  await expect(page.locator('#recommendations-action-summary')).toHaveText(
    '(0 visible — adjust filters)',
  );
});

test('selection updates the sticky action summary and submits the selected purchase', async ({ page }) => {
  const api = await mockApi(page);
  await openOpportunities(page);
  const selected = RECS.find((rec) => rec.id === 'r01') as SmokeRec;

  await selectRecommendation(page, selected.id);
  await expect(page.locator('.recommendations-filter-live')).toContainText('1 selected');
  await expect(page.locator('#recommendations-action-summary')).toContainText('across 1 cell');
  await page.getByRole('button', { name: 'Purchase 1 selected' }).click();
  await expect(page.getByRole('dialog', { name: 'Configure Purchase' })).toBeVisible();
  await page.getByRole('button', { name: 'Send for Approval' }).click();
  await page.locator('.modal-confirm-backdrop')
    .getByRole('button', { name: 'Send for approval' })
    .click();

  await expect.poll(() => callsTo(api, '/api/purchases/execute').length).toBe(1);
  const body = callsTo(api, '/api/purchases/execute')[0] as {
    recommendations: Array<Record<string, unknown>>;
  };
  expect(body.recommendations).toHaveLength(1);
  expect(body.recommendations[0]).toMatchObject({
    id: selected.id,
    provider: selected.provider,
    service: selected.service,
    term: selected.term,
    payment: selected.payment,
    count: selected.count,
    recommended_count: selected.count,
    selected: true,
    purchased: false,
  });
});

test('submits a plan with the selected recommendation snapshot', async ({ page }) => {
  const api = await mockApi(page);
  await openOpportunities(page);
  const selected = RECS.find((rec) => rec.id === 'r01') as SmokeRec;

  await selectRecommendation(page, selected.id);
  await page.getByRole('button', { name: 'Plan from 1 selected' }).click();
  const modal = page.getByRole('dialog', { name: 'Create Purchase Plan' });
  await expect(modal).toBeVisible();
  await expect(modal.getByLabel('Provider:')).toHaveValue(selected.provider);
  await expect(modal.getByLabel('Service:')).toHaveValue(selected.service);
  await expect(modal.getByLabel('Term:')).toHaveValue(String(selected.term));
  await expect(modal.getByLabel('Payment:')).toHaveValue(selected.payment);
  await expect(modal.locator('#plan-account-ids')).toHaveValue(selected.cloud_account_id);
  await modal.getByRole('button', { name: 'Save Plan' }).click();

  await expect.poll(() => callsTo(api, '/api/plans').length).toBe(1);
  const body = callsTo(api, '/api/plans')[0] as {
    provider: string;
    service: string;
    term: number;
    payment: string;
    target_accounts: string[];
    recommendations: SmokeRec[];
  };
  expect(body).toMatchObject({
    provider: selected.provider,
    service: selected.service,
    term: selected.term,
    payment: selected.payment,
    target_accounts: [selected.cloud_account_id],
  });
  expect(body.recommendations).toEqual([selected]);
  await expect(modal).toBeHidden();
});
