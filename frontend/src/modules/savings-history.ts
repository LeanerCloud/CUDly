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
 * Load savings history data based on selected period
 */
export async function loadSavingsHistory(): Promise<void> {
    const periodSelect = document.getElementById('savings-period') as HTMLSelectElement;
    const chartContainer = document.getElementById('savings-history-chart')?.parentElement;
    const emptyEl = document.getElementById('savings-history-empty');
    const statsEl = document.getElementById('savings-stats');

    if (!periodSelect) return;

    const period = periodSelect.value;
    const { start, end, interval } = getPeriodDates(period);

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
 * Get start/end dates and interval based on period selection
 */
function getPeriodDates(period: string): { start: Date; end: Date; interval: 'hourly' | 'daily' | 'weekly' | 'monthly' } {
    const end = new Date();
    const start = new Date();
    let interval: 'hourly' | 'daily' | 'weekly' | 'monthly' = 'hourly';

    switch (period) {
        case '24h':
            start.setHours(start.getHours() - 24);
            interval = 'hourly';
            break;
        case '7d':
            start.setDate(start.getDate() - 7);
            interval = 'hourly';
            break;
        case '30d':
            start.setDate(start.getDate() - 30);
            interval = 'daily';
            break;
        case '90d':
            start.setDate(start.getDate() - 90);
            interval = 'daily';
            break;
        default:
            start.setDate(start.getDate() - 7);
            interval = 'hourly';
    }

    return { start, end, interval };
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
 * Initialize savings history event listeners
 */
export function initSavingsHistory(): void {
    const periodSelect = document.getElementById('savings-period');
    const refreshBtn = document.getElementById('refresh-savings-btn');

    if (periodSelect) {
        periodSelect.addEventListener('change', loadSavingsHistory);
    }

    if (refreshBtn) {
        refreshBtn.addEventListener('click', loadSavingsHistory);
    }
}

// Export for use in other modules
export { savingsChart };
