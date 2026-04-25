/**
 * Savings History module - displays historical savings chart
 */

import { Chart, registerables } from 'chart.js';
import { getSavingsAnalytics, type SavingsAnalyticsResponse, type SavingsDataPoint } from '../api';

// Register Chart.js components
Chart.register(...registerables);

// Chart instance
let savingsChart: Chart | null = null;

/**
 * Load savings history data based on the unified date-range picker
 * (#history-start / #history-end inputs — see issue #55). Falls back
 * to a 7-day window if the inputs are missing or empty so callers
 * that mount the section before the controls (e.g. plan-history
 * deep-link) still get sensible data.
 */
export async function loadSavingsHistory(): Promise<void> {
    const chartContainer = document.getElementById('savings-history-chart')?.parentElement;
    const emptyEl = document.getElementById('savings-history-empty');
    const statsEl = document.getElementById('savings-stats');

    const { start, end, interval } = getRangeFromInputs();

    try {
        const data = await getSavingsAnalytics({
            start: start.toISOString(),
            end: end.toISOString(),
            interval,
        });

        if (!data.data_points || data.data_points.length === 0) {
            showEmptyState(chartContainer, emptyEl, statsEl);
            return;
        }

        // Show chart, hide empty state
        if (chartContainer) chartContainer.classList.remove('hidden');
        if (emptyEl) emptyEl.classList.add('hidden');
        if (statsEl) statsEl.classList.remove('hidden');

        renderSavingsStats(data);
        renderSavingsChart(data.data_points, interval);
    } catch (error) {
        console.error('Failed to load savings history:', error instanceof Error ? error.message : 'Unknown error');
        showEmptyState(chartContainer, emptyEl, statsEl);
    }
}

/**
 * Show empty state when no data is available
 */
function showEmptyState(
    chartContainer: HTMLElement | null | undefined,
    emptyEl: HTMLElement | null,
    statsEl: HTMLElement | null
): void {
    if (chartContainer) chartContainer.classList.add('hidden');
    if (emptyEl) emptyEl.classList.remove('hidden');
    if (statsEl) statsEl.classList.add('hidden');

    // Clear any existing chart
    if (savingsChart) {
        savingsChart.destroy();
        savingsChart = null;
    }
}

/**
 * Read the shared date-range picker (`#history-start` / `#history-end`
 * — populated by initHistoryDateRange + applyHistoryPreset in
 * history.ts) and derive the (start, end, interval) tuple.
 *
 * Interval picks `hourly` for spans up to 7 days and `daily` beyond,
 * matching the previous Period-dropdown behaviour:
 *   - 7d preset  → 7-day span  → hourly
 *   - 30d preset → 30-day span → daily
 *   - 90d preset → 90-day span → daily
 *
 * Falls back to a 7-day window if inputs are missing/empty so callers
 * that fire before initHistoryDateRange still get a sensible default.
 */
function getRangeFromInputs(): { start: Date; end: Date; interval: 'hourly' | 'daily' | 'weekly' | 'monthly' } {
    const startInput = document.getElementById('history-start') as HTMLInputElement | null;
    const endInput = document.getElementById('history-end') as HTMLInputElement | null;

    const end = parseDateOrNow(endInput?.value);
    let start: Date;
    if (startInput?.value) {
        start = new Date(startInput.value);
        if (Number.isNaN(start.getTime())) {
            start = defaultStart(end);
        }
    } else {
        start = defaultStart(end);
    }

    // Guard against an inverted range (start > end) — clamp start back
    // 7 days from end so the API call stays well-formed.
    if (start.getTime() > end.getTime()) {
        start = defaultStart(end);
    }

    const spanMs = end.getTime() - start.getTime();
    const spanDays = spanMs / (1000 * 60 * 60 * 24);
    const interval: 'hourly' | 'daily' = spanDays <= 7 ? 'hourly' : 'daily';

    return { start, end, interval };
}

function parseDateOrNow(value: string | undefined): Date {
    if (!value) return new Date();
    const d = new Date(value);
    return Number.isNaN(d.getTime()) ? new Date() : d;
}

function defaultStart(end: Date): Date {
    const start = new Date(end);
    start.setDate(start.getDate() - 7);
    return start;
}

/**
 * Render savings statistics
 */
function renderSavingsStats(data: SavingsAnalyticsResponse): void {
    const periodSavingsEl = document.getElementById('period-savings');
    const avgHourlySavingsEl = document.getElementById('avg-hourly-savings');
    const peakSavingsEl = document.getElementById('peak-savings');

    const summary = data.summary;
    const dataPoints = data.data_points || [];

    // Calculate totals from data points (sum of hourly savings)
    let totalSavings = 0;
    let peakSavings = 0;

    for (const dp of dataPoints) {
        const savings = dp.total_savings || 0;
        totalSavings += savings;
        if (savings > peakSavings) {
            peakSavings = savings;
        }
    }

    const avgPerPeriod = dataPoints.length > 0 ? totalSavings / dataPoints.length : 0;

    // Use summary if available, otherwise use calculated values
    const displayTotal = summary?.total_period_savings ?? totalSavings;
    const displayAvg = summary?.average_savings_per_period ?? avgPerPeriod;
    const displayPeak = summary?.peak_savings ?? peakSavings;

    if (periodSavingsEl) {
        periodSavingsEl.textContent = formatCurrency(displayTotal);
    }
    if (avgHourlySavingsEl) {
        avgHourlySavingsEl.textContent = `${formatCurrency(displayAvg)}/hr`;
    }
    if (peakSavingsEl) {
        peakSavingsEl.textContent = `${formatCurrency(displayPeak)}/hr`;
    }
}

/**
 * Format currency value
 */
function formatCurrency(value: number): string {
    if (value >= 1000) {
        return `$${(value / 1000).toFixed(2)}K`;
    }
    return `$${value.toFixed(2)}`;
}

/**
 * Render savings chart using Chart.js
 */
function renderSavingsChart(dataPoints: SavingsDataPoint[], interval: string): void {
    const ctx = document.getElementById('savings-history-chart') as HTMLCanvasElement;

    if (!ctx) {
        console.error('Canvas element not found: savings-history-chart');
        return;
    }

    // Format labels based on interval
    const labels = dataPoints.map(dp => {
        const date = new Date(dp.timestamp);
        if (interval === 'daily' || interval === 'weekly' || interval === 'monthly') {
            return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
        }
        return date.toLocaleString('en-US', {
            month: 'short',
            day: 'numeric',
            hour: 'numeric',
            hour12: true
        });
    });

    const savingsData = dataPoints.map(dp => dp.total_savings || 0);
    const cumulativeSavings = dataPoints.map(dp => dp.cumulative_savings || 0);

    if (savingsChart) {
        savingsChart.destroy();
    }

    savingsChart = new Chart(ctx, {
        type: 'line',
        data: {
            labels,
            datasets: [
                {
                    label: 'Period Savings',
                    data: savingsData,
                    borderColor: '#34a853',
                    backgroundColor: 'rgba(52, 168, 83, 0.1)',
                    fill: true,
                    tension: 0.3,
                    pointRadius: dataPoints.length > 50 ? 0 : 3,
                    pointHoverRadius: 5,
                    yAxisID: 'y',
                },
                {
                    label: 'Cumulative Savings',
                    data: cumulativeSavings,
                    borderColor: '#4285f4',
                    backgroundColor: 'rgba(66, 133, 244, 0.05)',
                    fill: false,
                    tension: 0.3,
                    borderDash: [5, 5],
                    pointRadius: dataPoints.length > 50 ? 0 : 2,
                    pointHoverRadius: 4,
                    yAxisID: 'y1',
                },
            ],
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            interaction: {
                intersect: false,
                mode: 'index',
            },
            scales: {
                x: {
                    display: true,
                    grid: {
                        display: false,
                    },
                    ticks: {
                        maxTicksLimit: 8,
                        maxRotation: 0,
                    },
                },
                y: {
                    type: 'linear',
                    display: true,
                    position: 'left',
                    beginAtZero: true,
                    grid: {
                        color: 'rgba(0, 0, 0, 0.05)',
                    },
                    ticks: {
                        callback: function(value: number | string) {
                            const numValue = typeof value === 'string' ? parseFloat(value) : value;
                            return `$${numValue.toFixed(2)}`;
                        },
                    },
                    title: {
                        display: true,
                        text: 'Savings per Period',
                    },
                },
                y1: {
                    type: 'linear',
                    display: true,
                    position: 'right',
                    beginAtZero: true,
                    grid: {
                        drawOnChartArea: false,
                    },
                    ticks: {
                        callback: function(value: number | string) {
                            const numValue = typeof value === 'string' ? parseFloat(value) : value;
                            if (numValue >= 1000) {
                                return `$${(numValue / 1000).toFixed(1)}K`;
                            }
                            return `$${numValue.toFixed(0)}`;
                        },
                    },
                    title: {
                        display: true,
                        text: 'Cumulative Savings',
                    },
                },
            },
            plugins: {
                legend: {
                    position: 'top',
                    labels: {
                        usePointStyle: true,
                        boxWidth: 8,
                    },
                },
                tooltip: {
                    callbacks: {
                        label: function(context) {
                            const value = context.raw as number || 0;
                            if (context.datasetIndex === 1) {
                                // Cumulative savings
                                return `${context.dataset.label}: $${value.toFixed(2)}`;
                            }
                            return `${context.dataset.label}: $${value.toFixed(4)}/hr`;
                        },
                    },
                },
            },
        },
    });
}

/**
 * Initialize savings history event listeners.
 *
 * The standalone Period dropdown was removed in #55 — the unified
 * date-range picker (handled in app.ts) now drives loadSavingsHistory
 * alongside the Purchase events table. The Refresh button is still
 * wired here because it's local to the savings card and the chart
 * recreation lives in this module.
 */
export function initSavingsHistory(): void {
    const refreshBtn = document.getElementById('refresh-savings-btn');

    if (refreshBtn) {
        refreshBtn.addEventListener('click', loadSavingsHistory);
    }
}

// Export for use in other modules
export { savingsChart };
