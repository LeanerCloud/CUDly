package common

// Archera partnership surface: the canonical signup link, the enrollment
// window, and the two disclosures CUDly commits to keeping visible EVERYWHERE
// the integration is surfaced.
//
// These live in pkg/common because more than one binary surfaces the offer
// after a purchase completes and each previously carried its own copy of the
// wording: the CLI (cmd/multi_service_stats.go's printArcheraPitch) and now
// the MCP server (mcp/tools/purchase.go). The frontend has its own TypeScript
// copy in frontend/src/archera.ts, which cannot import Go; that one is
// cross-language duplication of the unavoidable kind, and both sides carry a
// comment pointing at the other.
//
// The disclosures are not decoration and must not be dropped from any surface
// that shows the signup link. They are regression-test guarded, because a
// sponsored recommendation presented without stating the sponsorship (or
// without stating that the product works fine without it) misrepresents the
// relationship to the user.

// ArcheraSignupURL is the Archera signup link carrying CUDly attribution.
// Shared with the CLI and kept identical to the frontend's
// ARCHERA_SIGNUP_URL (frontend/src/archera.ts).
//
// NOTE: internal/email/templates.go currently sends a DIFFERENT link
// (https://archera.ai/signup?mode=cudly). That divergence predates this
// constant and is deliberately left alone here rather than silently
// normalized, because the two may be distinct attribution paths on Archera's
// side; reconciling them is a partnership question, not a refactor.
const ArcheraSignupURL = "https://www.archera.ai/cudly"

// ArcheraEnrollmentWindowDays is how long after a purchase a buyer has to
// enroll that commitment in Archera's coverage.
const ArcheraEnrollmentWindowDays = 7

// ArcheraNonGatingDisclosure states that the offer is optional and that CUDly
// is fully functional without it. Disclosure 1 of 2.
const ArcheraNonGatingDisclosure = "This is entirely optional. CUDly's purchase and management features " +
	"work fully without Archera."

// ArcheraSponsorshipDisclosure states the financial relationship behind the
// recommendation. Disclosure 2 of 2.
const ArcheraSponsorshipDisclosure = "For full disclosure, Archera sponsors CUDly's Open Source development " +
	"from a fraction of their insurance premiums."

// ArcheraPitch is the offer itself: what the coverage does and why a buyer
// who just committed spend might want it.
const ArcheraPitch = "Want to push your coverage to 100% without the risk that a future capacity decrease " +
	"leaves you paying for commitments you no longer use? You can buy underutilization insurance for " +
	"Reserved Instances and Savings Plans from Archera."
