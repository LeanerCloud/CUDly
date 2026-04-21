/**
 * API module barrel export
 * Re-exports all API functions and types for backward compatibility
 */

// Re-export all types
export type {
  Provider,
  PaymentOption,
  RampSchedule,
  User,
  LoginResponse,
  DashboardSummary,
  UpcomingPurchase,
  Recommendation,
  RecommendationFilters,
  Plan,
  CreatePlanRequest,
  PurchaseHistory,
  HistoryFilters,
  Config,
  ServiceConfig,
  PublicInfo,
  PurchaseResult,
  PurchaseDetails,
  PlannedPurchasesResponse,
  PlannedPurchase,
  APIUser,
  CreateUserRequest,
  UpdateUserRequest,
  APIGroup,
  Permission,
  CreateGroupRequest,
  UpdateGroupRequest,
  APIKeyInfo,
  CreateAPIKeyRequest,
  CreateAPIKeyResponse,
  GetAPIKeysResponse,
  SavingsAnalyticsResponse,
  SavingsAnalyticsSummary,
  SavingsDataPoint,
  SavingsBreakdownResponse,
  SavingsBreakdownValue,
  SavingsAnalyticsFilters,
  ConvertibleRI,
  RIUtilization,
  ReshapeRecommendation,
  OfferingOption,
  ExchangeQuoteRequest,
  ExchangeQuoteSummary,
  ExchangeExecuteRequest,
  ExchangeTarget,
  ExchangeResult,
  RIExchangeConfig,
  RIExchangeHistoryRecord
} from './types';

// Re-export client functions
export {
  initAuth,
  setAuthToken,
  setCsrfToken,
  setApiKey,
  isAuthenticated,
  clearAuth,
  getAuthHeaders,
  apiRequest,
  base64Encode
} from './client';

// Re-export auth functions
export {
  login,
  logout,
  getCurrentUser,
  requestPasswordReset,
  resetPassword,
  checkAdminExists,
  setupAdmin,
  changePassword,
  getPublicInfo
} from './auth';

// Re-export dashboard functions
export {
  getDashboardSummary,
  getUpcomingPurchases
} from './dashboard';

// Re-export recommendations functions
export {
  getRecommendations,
  refreshRecommendations
} from './recommendations';

// Re-export plans functions
export {
  getPlans,
  getPlan,
  createPlan,
  updatePlan,
  patchPlan,
  deletePlan
} from './plans';

// Re-export history functions
export {
  getHistory,
  getSavingsAnalytics,
  getSavingsBreakdown
} from './history';

// Re-export purchases functions
export {
  executePurchase,
  getPurchaseDetails,
  cancelPurchase,
  getPlannedPurchases,
  pausePlannedPurchase,
  resumePlannedPurchase,
  runPlannedPurchase,
  deletePlannedPurchase,
  createPlannedPurchases
} from './purchases';

// Re-export users functions
export {
  listUsers,
  getUser,
  createUser,
  updateUser,
  deleteUser
} from './users';

// Re-export groups functions
export {
  listGroups,
  getGroup,
  createGroup,
  updateGroup,
  deleteGroup
} from './groups';

// Re-export apikeys functions
export {
  getApiKeys,
  createApiKey,
  revokeApiKey,
  deleteApiKey
} from './apikeys';

// Re-export settings functions
export {
  getConfig,
  updateConfig,
  updateServiceConfig
} from './settings';

// Re-export RI exchange functions
export {
  listConvertibleRIs,
  getRIUtilization,
  getReshapeRecommendations,
  getExchangeQuote,
  executeExchange,
  getRIExchangeConfig,
  updateRIExchangeConfig,
  getRIExchangeHistory
} from './riexchange';

// Re-export registrations functions and types
export type { AccountRegistration } from './registrations';
export {
  listRegistrations,
  getRegistration,
  approveRegistration,
  rejectRegistration,
  deleteRegistration
} from './registrations';

// Re-export accounts functions and types
export type {
  CloudAccount,
  CloudAccountRequest,
  AccountListFilters,
  AccountCredentialsRequest,
  AccountTestResult,
  AccountServiceOverride,
  AccountServiceOverrideRequest,
  OrgDiscoveryResult,
  FederationIaCResponse
} from './accounts';

export {
  listAccounts,
  createAccount,
  createSelfAccount,
  getAccount,
  updateAccount,
  deleteAccount,
  saveAccountCredentials,
  testAccountCredentials,
  listAccountServiceOverrides,
  saveAccountServiceOverride,
  deleteAccountServiceOverride,
  discoverOrgAccounts,
  listPlanAccounts,
  setPlanAccounts,
  getFederationIaC
} from './accounts';
