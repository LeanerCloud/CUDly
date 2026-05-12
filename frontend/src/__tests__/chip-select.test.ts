/**
 * chip-select component tests (issue #344 T1).
 */

import { createChipSelect, type ChipSelectOption } from '../lib/chip-select';

const SHORT_OPTIONS: ChipSelectOption[] = [
  { value: '', label: 'All' },
  { value: 'aws', label: 'AWS' },
  { value: 'azure', label: 'Azure' },
  { value: 'gcp', label: 'GCP' },
];

// Long enough to trigger the in-popover search filter (>8 options).
const LONG_OPTIONS: ChipSelectOption[] = Array.from({ length: 12 }, (_, i) => ({
  value: `acct-${i}`,
  label: `Account ${i}`,
}));

describe('createChipSelect', () => {
  afterEach(() => {
    while (document.body.firstChild) document.body.removeChild(document.body.firstChild);
  });

  test('renders a trigger with label + current value', () => {
    const { root } = createChipSelect({
      label: 'Provider',
      options: SHORT_OPTIONS,
      value: 'aws',
      onChange: () => {},
    });
    document.body.appendChild(root);

    const trigger = root.querySelector<HTMLButtonElement>('.chip-select');
    expect(trigger).not.toBeNull();
    expect(trigger?.getAttribute('aria-haspopup')).toBe('listbox');
    expect(trigger?.getAttribute('aria-expanded')).toBe('false');
    expect(root.querySelector('.chip-select-label')?.textContent).toBe('Provider:');
    expect(root.querySelector('.chip-select-value')?.textContent).toBe('AWS');
  });

  test('clicking the trigger opens the menu; clicking again closes it', () => {
    const { root } = createChipSelect({
      label: 'Provider',
      options: SHORT_OPTIONS,
      value: '',
      onChange: () => {},
    });
    document.body.appendChild(root);

    const trigger = root.querySelector<HTMLButtonElement>('.chip-select')!;
    const menu = root.querySelector<HTMLDivElement>('.chip-select-menu')!;

    expect(menu.classList.contains('hidden')).toBe(true);
    trigger.click();
    expect(menu.classList.contains('hidden')).toBe(false);
    expect(trigger.getAttribute('aria-expanded')).toBe('true');

    trigger.click();
    expect(menu.classList.contains('hidden')).toBe(true);
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
  });

  test('selecting an option fires onChange and updates the trigger label', () => {
    const onChange = jest.fn();
    const { root, getValue } = createChipSelect({
      label: 'Provider',
      options: SHORT_OPTIONS,
      value: '',
      onChange,
    });
    document.body.appendChild(root);

    const trigger = root.querySelector<HTMLButtonElement>('.chip-select')!;
    trigger.click();

    // Find the Azure option and click it.
    const azureOption = Array.from(
      root.querySelectorAll<HTMLLIElement>('.chip-select-option'),
    ).find((li) => li.dataset['value'] === 'azure')!;

    // mousedown is the event the component listens for (avoids focus race).
    azureOption.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));

    expect(onChange).toHaveBeenCalledWith('azure');
    expect(getValue()).toBe('azure');
    expect(root.querySelector('.chip-select-value')?.textContent).toBe('Azure');
    // Menu closes on selection.
    expect(root.querySelector('.chip-select-menu')?.classList.contains('hidden')).toBe(true);
  });

  test('Escape closes the menu', () => {
    const { root } = createChipSelect({
      label: 'Provider',
      options: SHORT_OPTIONS,
      value: '',
      onChange: () => {},
    });
    document.body.appendChild(root);

    const trigger = root.querySelector<HTMLButtonElement>('.chip-select')!;
    trigger.click();
    expect(root.querySelector('.chip-select-menu')?.classList.contains('hidden')).toBe(false);

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(root.querySelector('.chip-select-menu')?.classList.contains('hidden')).toBe(true);
  });

  test('click outside closes the menu', () => {
    const { root } = createChipSelect({
      label: 'Provider',
      options: SHORT_OPTIONS,
      value: '',
      onChange: () => {},
    });
    document.body.appendChild(root);

    const outside = document.createElement('button');
    outside.textContent = 'somewhere else';
    document.body.appendChild(outside);

    const trigger = root.querySelector<HTMLButtonElement>('.chip-select')!;
    trigger.click();
    expect(root.querySelector('.chip-select-menu')?.classList.contains('hidden')).toBe(false);

    outside.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
    expect(root.querySelector('.chip-select-menu')?.classList.contains('hidden')).toBe(true);
  });

  test('search input renders only when options exceed the threshold', () => {
    // Short list — no search input.
    const short = createChipSelect({
      label: 'Provider',
      options: SHORT_OPTIONS,
      value: '',
      onChange: () => {},
    });
    document.body.appendChild(short.root);
    short.root.querySelector<HTMLButtonElement>('.chip-select')!.click();
    expect(short.root.querySelector('.chip-select-search')).toBeNull();

    // Long list — search input appears.
    document.body.removeChild(short.root);
    const long = createChipSelect({
      label: 'Account',
      options: LONG_OPTIONS,
      value: '',
      onChange: () => {},
    });
    document.body.appendChild(long.root);
    long.root.querySelector<HTMLButtonElement>('.chip-select')!.click();
    expect(long.root.querySelector('.chip-select-search')).not.toBeNull();
  });

  test('typing in search filters options case-insensitively', () => {
    const { root } = createChipSelect({
      label: 'Account',
      options: LONG_OPTIONS,
      value: '',
      onChange: () => {},
    });
    document.body.appendChild(root);

    root.querySelector<HTMLButtonElement>('.chip-select')!.click();
    const search = root.querySelector<HTMLInputElement>('.chip-select-search')!;
    search.value = 'account 1'; // matches Account 1, 10, 11
    search.dispatchEvent(new Event('input', { bubbles: true }));

    const visibleOptions = root.querySelectorAll<HTMLLIElement>('.chip-select-option');
    expect(visibleOptions.length).toBe(3);
    expect(Array.from(visibleOptions).map((li) => li.textContent)).toEqual([
      'Account 1', 'Account 10', 'Account 11',
    ]);
  });

  test('search with no matches shows the empty hint', () => {
    const { root } = createChipSelect({
      label: 'Account',
      options: LONG_OPTIONS,
      value: '',
      onChange: () => {},
    });
    document.body.appendChild(root);

    root.querySelector<HTMLButtonElement>('.chip-select')!.click();
    const search = root.querySelector<HTMLInputElement>('.chip-select-search')!;
    search.value = 'zzzzz nope';
    search.dispatchEvent(new Event('input', { bubbles: true }));

    expect(root.querySelector('.chip-select-empty')?.textContent).toBe('No matches');
  });

  test('setValue + setOptions update the trigger and the open menu', () => {
    const { root, setValue, setOptions, getValue } = createChipSelect({
      label: 'Account',
      options: [{ value: 'a', label: 'A' }],
      value: 'a',
      onChange: () => {},
    });
    document.body.appendChild(root);
    expect(root.querySelector('.chip-select-value')?.textContent).toBe('A');

    setOptions([
      { value: 'b', label: 'B' },
      { value: 'c', label: 'C' },
    ]);
    // setOptions doesn't change the value automatically; trigger now shows
    // the empty fallback because 'a' isn't in the new options.
    expect(getValue()).toBe('a');
    expect(root.querySelector('.chip-select-value')?.textContent).toBe('(any)');

    setValue('c');
    expect(getValue()).toBe('c');
    expect(root.querySelector('.chip-select-value')?.textContent).toBe('C');
  });

  test('ArrowDown on the trigger opens the menu', () => {
    const { root } = createChipSelect({
      label: 'Provider',
      options: SHORT_OPTIONS,
      value: '',
      onChange: () => {},
    });
    document.body.appendChild(root);

    const trigger = root.querySelector<HTMLButtonElement>('.chip-select')!;
    expect(root.querySelector('.chip-select-menu')?.classList.contains('hidden')).toBe(true);
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    expect(root.querySelector('.chip-select-menu')?.classList.contains('hidden')).toBe(false);
  });
});
