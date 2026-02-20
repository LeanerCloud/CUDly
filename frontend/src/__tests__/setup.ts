/**
 * Jest test setup
 */
import '@testing-library/jest-dom';
import { TextEncoder, TextDecoder } from 'util';
import { webcrypto } from 'crypto';

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
