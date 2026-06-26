/**
 * Jest test setup
 */
import '@testing-library/jest-dom';
import { TextEncoder, TextDecoder } from 'util';
import { webcrypto } from 'crypto';
import structuredClonePolyfill from '@ungap/structured-clone';

// Polyfill TextEncoder/TextDecoder for Node.js environment
global.TextEncoder = TextEncoder;
global.TextDecoder = TextDecoder as typeof global.TextDecoder;

// Polyfill crypto.subtle for Node.js environment (for SHA256 hashing in api.ts)
Object.defineProperty(global, 'crypto', {
  value: webcrypto,
  writable: true
});

// Create mock localStorage with jest.fn()
const localStorageMock = {
  getItem: jest.fn((_key: string): string | null => null),
  setItem: jest.fn((_key: string, _value: string): void => undefined),
  removeItem: jest.fn((_key: string): void => undefined),
  clear: jest.fn((): void => undefined),
  length: 0,
  key: jest.fn((_index: number): string | null => null)
};

Object.defineProperty(global, 'localStorage', {
  value: localStorageMock,
  writable: true
});

// Create mock sessionStorage with jest.fn() (mirrors localStorage mock)
const sessionStorageMock = {
  getItem: jest.fn((_key: string): string | null => null),
  setItem: jest.fn((_key: string, _value: string): void => undefined),
  removeItem: jest.fn((_key: string): void => undefined),
  clear: jest.fn((): void => undefined),
  length: 0,
  key: jest.fn((_index: number): string | null => null)
};

Object.defineProperty(global, 'sessionStorage', {
  value: sessionStorageMock,
  writable: true
});

// Mock fetch
const fetchMock = jest.fn() as jest.Mock;
global.fetch = fetchMock;

// Mock Chart.js
Object.defineProperty(global, 'Chart', {
  value: jest.fn().mockImplementation(() => ({
    destroy: jest.fn()
  })),
  writable: true
});

// Polyfill structuredClone for jsdom (jest-environment-jsdom does not expose
// the Node.js global structuredClone to the window scope). The utils.ts
// deepClone function delegates to structuredClone; without this the suite
// throws "ReferenceError: structuredClone is not defined" (finding 11-N1).
//
// Prefer the environment-provided native implementation when present; only
// polyfill when absent. The polyfill is @ungap/structured-clone, a faithful
// implementation of the HTML structured clone algorithm (preserves
// undefined-valued properties, Date, Map, Set, RegExp, cycles; throws
// TypeError on functions), unlike the previous JSON round-trip which
// validated weaker semantics than production browsers (TEST-07). When the
// host Node has a native structuredClone, @ungap delegates to it; otherwise
// it uses its own serialize/deserialize. In jsdom-on-jest, Date/Map/Set
// primordials are shared with Node so `instanceof` keeps working on cloned
// values regardless of which path runs.
if (typeof globalThis.structuredClone === 'undefined') {
  globalThis.structuredClone = (<T>(value: T, options?: StructuredSerializeOptions): T => {
    if (options?.transfer?.length) {
      throw new Error('structuredClone polyfill does not support the transfer option');
    }
    return structuredClonePolyfill(value);
  }) as typeof structuredClone;
}

// Mock alert and confirm
global.alert = jest.fn();
global.confirm = jest.fn(() => true);

// Reset mocks before each test
beforeEach(() => {
  jest.clearAllMocks();
  localStorageMock.getItem.mockReturnValue(null);
  sessionStorageMock.getItem.mockReturnValue(null);
  fetchMock.mockReset();
});

// Clean up after each test
afterEach(() => {
  document.body.innerHTML = '';
});

// Export for use in tests
export { localStorageMock, sessionStorageMock, fetchMock };
