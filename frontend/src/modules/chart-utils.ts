/**
 * Shared chart utility helpers used by multiple chart modules.
 */

/**
 * Format a millisecond timestamp for a savings-trend x-axis tick label.
 * The intervalHint drives granularity: hourly shows date+time, everything else shows date only.
 * Exported for unit testing.
 */
export function formatTrendAxisTick(tsMs: number, intervalHint: 'hourly' | 'daily' | 'weekly'): string {
    const d = new Date(tsMs);
    if (intervalHint === 'hourly') {
        return d.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false });
    }
    return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
}
