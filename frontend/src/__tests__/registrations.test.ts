/**
 * Registrations module — default-filter behaviour (refs #45).
 *
 * The Account Registrations status dropdown defaults to "Pending" in
 * index.html. `loadRegistrations` reads that select and forwards the value
 * to the API, so on first paint the panel must request only pending rows.
 */
import { loadRegistrations } from '../modules/registrations';

jest.mock('../api', () => ({
  listRegistrations: jest.fn(),
}));

import * as api from '../api';

describe('loadRegistrations default filter', () => {
  beforeEach(() => {
    document.body.replaceChildren();
    jest.clearAllMocks();
  });

  function buildDOM(initialFilterValue: string): void {
    const container = document.createElement('div');
    container.id = 'registrations-list';
    document.body.appendChild(container);

    const select = document.createElement('select');
    select.id = 'registrations-status-filter';
    for (const v of ['', 'pending', 'approved', 'rejected']) {
      const opt = document.createElement('option');
      opt.value = v;
      // Mirror index.html: "pending" carries the `selected` attribute.
      if (v === 'pending') opt.defaultSelected = true;
      select.appendChild(opt);
    }
    select.value = initialFilterValue;
    document.body.appendChild(select);
  }

  test('passes "pending" to listRegistrations on first load', async () => {
    buildDOM('pending');
    (api.listRegistrations as jest.Mock).mockResolvedValue([]);

    await loadRegistrations();

    expect(api.listRegistrations).toHaveBeenCalledTimes(1);
    expect(api.listRegistrations).toHaveBeenCalledWith('pending');
  });

  test('passes undefined when the user picks "All" (empty value)', async () => {
    buildDOM('');
    (api.listRegistrations as jest.Mock).mockResolvedValue([]);

    await loadRegistrations();

    expect(api.listRegistrations).toHaveBeenCalledWith(undefined);
  });
});
