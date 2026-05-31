let chartInstance = null;

export function destroyChart() {
  if (chartInstance) {
    chartInstance.destroy();
    chartInstance = null;
  }
}

/**
 * @param {HTMLCanvasElement} canvas
 * @param {{ labels: string[], values: number[], bookTitle?: string }} series
 * @param {{ xLabel?: string, showTrend?: boolean }} options
 */
export function renderProgressChart(canvas, series, options = {}) {
  const ctx = canvas.getContext('2d');
  destroyChart();

  const datasets = [
    {
      label: series.bookTitle ? `${series.bookTitle} (%)` : 'Book completion (%)',
      data: series.values,
      borderColor: '#2563eb',
      backgroundColor: 'rgba(37, 99, 235, 0.08)',
      borderWidth: 2,
      fill: true,
      stepped: true,
      pointRadius: 4,
      pointHoverRadius: 6,
      pointBackgroundColor: '#dc2626',
      tension: 0,
    },
  ];

  if (options.showTrend && series.values.length >= 2) {
    datasets.push({
      label: 'Trend (linear)',
      data: series.values,
      borderColor: 'rgba(16, 185, 129, 0.6)',
      borderWidth: 1.5,
      borderDash: [6, 4],
      pointRadius: 0,
      stepped: false,
      fill: false,
      tension: 0.35,
    });
  }

  chartInstance = new Chart(ctx, {
    type: 'line',
    data: {
      labels: series.labels,
      datasets,
    },
    options: {
      responsive: true,
      maintainAspectRatio: true,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: { display: datasets.length > 1 },
        tooltip: {
          callbacks: {
            label(context) {
              const v = context.parsed.y;
              return `${context.dataset.label}: ${v?.toFixed?.(1) ?? v}%`;
            },
          },
        },
      },
      scales: {
        y: {
          beginAtZero: true,
          max: 100,
          title: { display: true, text: 'Progress P(t) — %' },
        },
        x: {
          title: {
            display: true,
            text: options.xLabel || 'Timeline',
          },
          ticks: {
            maxRotation: 45,
            minRotation: 0,
            autoSkip: true,
            maxTicksLimit: 12,
          },
        },
      },
    },
  });

  return chartInstance;
}

export function computeStats(values) {
  if (!values.length) return null;
  const first = values[0];
  const last = values[values.length - 1];
  const delta = last - first;
  const sessions = values.filter((v, i) => i === 0 || v > values[i - 1]).length;
  return {
    startPercent: first,
    currentPercent: last,
    totalGain: delta,
    readingSessions: Math.max(0, sessions - 1),
  };
}
