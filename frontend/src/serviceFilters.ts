/**
 * Per-service recommendation-filter controls for the Settings → Service
 * Defaults cards.
 *
 * Exposes the CLI filter knobs (include/exclude engines, instance types,
 * regions, and the --min-count threshold) in the GUI. The controls are
 * injected into each service card at runtime (keyed off SERVICE_FIELDS) so a
 * single code path drives every card instead of ~20 hand-written HTML blocks,
 * and so the inject / populate / read logic stays unit-testable in isolation.
 *
 * The backend already persists these fields on config.ServiceConfig and
 * applies them server-side (engines/types/regions in
 * scheduler.filterRecsByResolvedConfigs; min_count likewise). This module is
 * purely the missing GUI surface.
 *
 * All DOM construction goes through createElement + .value assignment (never
 * innerHTML), so user-entered values cannot inject markup.
 */

import type { ServiceConfig } from './api/types';

/** Identifies a single service card. Subset of a SERVICE_FIELDS entry. */
export interface ServiceFilterTarget {
  provider: string;
  service: string;
  /** ID of the card's term <select>; used to locate the card container. */
  termId: string;
}

/** The four list-filter dimensions plus the numeric min-count input. */
interface FilterFieldSpec {
  /** Suffix appended to `${provider}-${service}-` to form the input id. */
  key: string;
  label: string;
  placeholder: string;
}

const LIST_FIELDS: readonly FilterFieldSpec[] = [
  { key: 'include-engines', label: 'Include engines', placeholder: 'e.g. mysql, postgres' },
  { key: 'exclude-engines', label: 'Exclude engines', placeholder: 'e.g. aurora-mysql' },
  { key: 'include-types', label: 'Include instance types', placeholder: 'e.g. db.r6g.large' },
  { key: 'exclude-types', label: 'Exclude instance types', placeholder: 'e.g. db.t3.micro' },
  { key: 'include-regions', label: 'Include regions', placeholder: 'e.g. us-east-1, eu-west-1' },
  { key: 'exclude-regions', label: 'Exclude regions', placeholder: 'e.g. ap-south-1' },
] as const;

const MIN_COUNT_KEY = 'min-count';

/** Marker class on the injected panel so injection is idempotent. */
const PANEL_CLASS = 'service-filter-panel';

/** Returns the deterministic element id for a card's filter input. */
export function filterInputId(provider: string, service: string, key: string): string {
  return `${provider}-${service}-${key}`;
}

/** All input ids this module owns for a given card (for dirty tracking). */
export function serviceFilterInputIds(t: ServiceFilterTarget): string[] {
  const ids = LIST_FIELDS.map((f) => filterInputId(t.provider, t.service, f.key));
  ids.push(filterInputId(t.provider, t.service, MIN_COUNT_KEY));
  return ids;
}

/**
 * Parse a comma/whitespace-separated list into a trimmed, de-duplicated,
 * lowercased string array. Empty input yields []. Lowercasing matches the
 * server-side engine comparison (case-insensitive) and the CLI's normalisation;
 * regions/types are already lowercase by AWS convention.
 */
export function parseCsvList(raw: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(/[,\s]+/)) {
    const v = part.trim().toLowerCase();
    if (v !== '' && !seen.has(v)) {
      seen.add(v);
      out.push(v);
    }
  }
  return out;
}

/** Joins a string array back into the comma-separated display form. */
export function joinCsvList(values: string[] | undefined): string {
  return (values ?? []).join(', ');
}

function makeLabeledInput(
  id: string,
  labelText: string,
  type: 'text' | 'number',
  placeholder: string,
): HTMLLabelElement {
  const label = document.createElement('label');
  label.className = 'service-filter-field';
  label.append(document.createTextNode(`${labelText}: `));
  const input = document.createElement('input');
  input.type = type;
  input.id = id;
  if (placeholder) input.placeholder = placeholder;
  if (type === 'number') {
    input.min = '0';
    input.step = '1';
  }
  label.appendChild(input);
  return label;
}

/**
 * Inject the filter panel into a single service card. Idempotent: a card that
 * already carries the panel is left untouched (so re-running on tab re-entry
 * doesn't duplicate controls). Returns true when a panel was created, false
 * when the card was missing or already had one.
 */
export function injectServiceFilterControls(t: ServiceFilterTarget): boolean {
  const anchor = document.getElementById(t.termId);
  const card = anchor?.closest('.service-default-card');
  if (!card) return false;
  if (card.querySelector(`.${PANEL_CLASS}`)) return false;

  const panel = document.createElement('details');
  panel.className = PANEL_CLASS;
  const summary = document.createElement('summary');
  summary.textContent = 'Recommendation filters';
  panel.appendChild(summary);

  for (const f of LIST_FIELDS) {
    panel.appendChild(
      makeLabeledInput(filterInputId(t.provider, t.service, f.key), f.label, 'text', f.placeholder),
    );
  }
  panel.appendChild(
    makeLabeledInput(
      filterInputId(t.provider, t.service, MIN_COUNT_KEY),
      'Min count (0 = no filter)',
      'number',
      '',
    ),
  );

  card.appendChild(panel);
  return true;
}

/** Populate a card's filter inputs from a persisted ServiceConfig (or clear). */
export function populateServiceFilterControls(t: ServiceFilterTarget, svc: ServiceConfig | undefined): void {
  const setList = (key: string, values: string[] | undefined): void => {
    const el = document.getElementById(filterInputId(t.provider, t.service, key)) as HTMLInputElement | null;
    if (el) el.value = joinCsvList(values);
  };
  setList('include-engines', svc?.include_engines);
  setList('exclude-engines', svc?.exclude_engines);
  setList('include-types', svc?.include_types);
  setList('exclude-types', svc?.exclude_types);
  setList('include-regions', svc?.include_regions);
  setList('exclude-regions', svc?.exclude_regions);

  const minEl = document.getElementById(
    filterInputId(t.provider, t.service, MIN_COUNT_KEY),
  ) as HTMLInputElement | null;
  if (minEl) minEl.value = String(svc?.min_count ?? 0);
}

/** Result of reading a card's filter inputs. */
export interface ServiceFilterValues {
  include_engines: string[];
  exclude_engines: string[];
  include_types: string[];
  exclude_types: string[];
  include_regions: string[];
  exclude_regions: string[];
  min_count: number;
}

/**
 * Validation error from reading the controls. message is user-facing.
 */
export interface ServiceFilterError {
  message: string;
}

/**
 * Read a card's filter inputs into a ServiceFilterValues. Returns an error
 * (instead of a value) when min-count is not a non-negative whole number, so
 * the caller can surface a targeted toast and abort the save rather than
 * silently coercing a bad value. When a card has no injected panel (controls
 * absent), every list is [] and min_count is 0 — the caller treats that as
 * "no filters", consistent with the disabled-by-default semantics.
 */
export function readServiceFilterControls(
  t: ServiceFilterTarget,
): ServiceFilterValues | ServiceFilterError {
  const getList = (key: string): string[] => {
    const el = document.getElementById(filterInputId(t.provider, t.service, key)) as HTMLInputElement | null;
    return el ? parseCsvList(el.value) : [];
  };

  const minEl = document.getElementById(
    filterInputId(t.provider, t.service, MIN_COUNT_KEY),
  ) as HTMLInputElement | null;
  let minCount = 0;
  if (minEl) {
    const raw = minEl.value.trim();
    if (raw !== '') {
      const n = Number(raw);
      if (!Number.isFinite(n) || !Number.isInteger(n) || n < 0) {
        return {
          message: `Min count for ${t.provider}/${t.service} must be a whole number ≥ 0`,
        };
      }
      minCount = n;
    }
  }

  return {
    include_engines: getList('include-engines'),
    exclude_engines: getList('exclude-engines'),
    include_types: getList('include-types'),
    exclude_types: getList('exclude-types'),
    include_regions: getList('include-regions'),
    exclude_regions: getList('exclude-regions'),
    min_count: minCount,
  };
}

/** Type guard distinguishing a successful read from a validation error. */
export function isServiceFilterError(
  v: ServiceFilterValues | ServiceFilterError,
): v is ServiceFilterError {
  return (v as ServiceFilterError).message !== undefined;
}
