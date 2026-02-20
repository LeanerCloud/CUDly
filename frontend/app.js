// CUDly - Cloud Commitment Optimizer Dashboard
// Configuration - API calls go through CloudFront /api/* path
const API_BASE = '/api';
let apiKey = localStorage.getItem('apiKey') || '';
let authToken = localStorage.getItem('authToken') || '';
let currentUser = null;
let currentProvider = 'all';
let currentRecommendations = [];
let selectedRecommendations = new Set();
let savingsChart = null;

// Initialize app
async function init() {
    if (!authToken && !apiKey) {
        showLoginModal();
        return;
    }

    try {
        await loadCurrentUser();
        await loadDashboard();
        setupEventListeners();
    } catch (error) {
        console.error('Init error:', error);
        if (error.message.includes('401') || error.message.includes('Unauthorized')) {
            showLoginModal();
        }
    }
}

// Setup event listeners
function setupEventListeners() {
    // Tab switching
    document.querySelectorAll('.tab-btn').forEach(btn => {
        btn.addEventListener('click', () => switchTab(btn.dataset.tab));
    });

    // Provider filter
    document.getElementById('provider').addEventListener('change', (e) => {
        currentProvider = e.target.value;
        loadDashboard();
    });

    // Forms
    document.getElementById('plan-form').addEventListener('submit', savePlan);
    document.getElementById('global-settings-form').addEventListener('submit', saveGlobalSettings);

    // Ramp schedule toggle
    document.querySelectorAll('input[name="ramp-schedule"]').forEach(radio => {
        radio.addEventListener('change', (e) => {
            const customConfig = document.getElementById('custom-ramp-config');
            customConfig.classList.toggle('hidden', e.target.value !== 'custom');
        });
    });
}

// Auth header helper
function getAuthHeaders() {
    const headers = { 'Content-Type': 'application/json' };
    if (authToken) {
        headers['Authorization'] = `Bearer ${authToken}`;
    } else if (apiKey) {
        headers['X-API-Key'] = apiKey;
    }
    return headers;
}

// Show login modal
async function showLoginModal() {
    // Fetch the API key secret URL from the public info endpoint
    let secretUrl = '';
    try {
        const response = await fetch(`${API_BASE}/info`);
        if (response.ok) {
            const data = await response.json();
            secretUrl = data.api_key_secret_url || '';
        }
    } catch (e) {
        console.log('Failed to fetch info endpoint:', e);
    }

    const secretLink = secretUrl
        ? `<a href="${secretUrl}" target="_blank" rel="noopener">Open API Key in Secrets Manager</a>`
        : `<a href="https://console.aws.amazon.com/secretsmanager/listsecrets?search=CUDlyAPIKey" target="_blank" rel="noopener">Search for CUDlyAPIKey in Secrets Manager</a>`;

    const modal = document.createElement('div');
    modal.id = 'login-modal';
    modal.innerHTML = `
        <div class="modal-overlay">
            <div class="modal-content">
                <h2>CUDly Login</h2>
                <div id="login-tabs" class="login-tabs">
                    <button type="button" class="login-tab active" data-mode="user">User Login</button>
                    <button type="button" class="login-tab" data-mode="api-key">API Key (Admin Setup)</button>
                </div>

                <form id="login-form">
                    <div id="user-login-fields">
                        <label>Email:
                            <input type="email" id="login-email" placeholder="admin@example.com" autocomplete="email">
                        </label>
                        <label>Password:
                            <input type="password" id="login-password" placeholder="Password" autocomplete="current-password">
                        </label>
                        <p class="help-text"><a href="#" onclick="showForgotPasswordForm(); return false;">Forgot password?</a></p>
                    </div>

                    <div id="api-key-login-fields" class="hidden">
                        <p class="modal-hint">
                            First-time setup: Use the API key to create an admin account.<br>
                            ${secretLink}<br>
                            <small>Click <strong>"Retrieve secret value"</strong> to reveal the key.</small>
                        </p>
                        <label>API Key:
                            <input type="text" id="login-api-key" placeholder="Enter API Key" autocomplete="off">
                        </label>
                        <div id="admin-setup-fields" class="hidden">
                            <hr>
                            <h3>Create Admin Account</h3>
                            <label>Admin Email:
                                <input type="email" id="admin-email" placeholder="admin@example.com">
                            </label>
                            <label>Admin Password:
                                <input type="password" id="admin-password" placeholder="Choose a password">
                            </label>
                            <label>Confirm Password:
                                <input type="password" id="admin-password-confirm" placeholder="Confirm password">
                            </label>
                        </div>
                    </div>

                    <div id="login-error" class="error-message hidden"></div>
                    <button type="submit" class="primary">Login</button>
                </form>
            </div>
        </div>
    `;
    document.body.appendChild(modal);

    // Tab switching
    modal.querySelectorAll('.login-tab').forEach(tab => {
        tab.addEventListener('click', () => {
            modal.querySelectorAll('.login-tab').forEach(t => t.classList.remove('active'));
            tab.classList.add('active');
            const mode = tab.dataset.mode;
            document.getElementById('user-login-fields').classList.toggle('hidden', mode !== 'user');
            document.getElementById('api-key-login-fields').classList.toggle('hidden', mode !== 'api-key');
        });
    });

    // API key input - check if admin exists
    document.getElementById('login-api-key').addEventListener('blur', async (e) => {
        const key = e.target.value.trim();
        if (key) {
            try {
                const response = await fetch(`${API_BASE}/auth/check-admin`, {
                    headers: { 'X-API-Key': key }
                });
                if (response.ok) {
                    const data = await response.json();
                    document.getElementById('admin-setup-fields').classList.toggle('hidden', data.admin_exists);
                }
            } catch (err) {
                console.log('Admin check failed:', err);
            }
        }
    });

    // Login form submission
    document.getElementById('login-form').addEventListener('submit', async (e) => {
        e.preventDefault();
        const errorDiv = document.getElementById('login-error');
        errorDiv.classList.add('hidden');

        const isApiKeyMode = document.querySelector('.login-tab.active').dataset.mode === 'api-key';

        try {
            if (isApiKeyMode) {
                const key = document.getElementById('login-api-key').value.trim();
                const adminEmail = document.getElementById('admin-email').value.trim();
                const adminPassword = document.getElementById('admin-password').value;
                const confirmPassword = document.getElementById('admin-password-confirm').value;

                if (adminEmail) {
                    // Create admin account
                    if (adminPassword !== confirmPassword) {
                        throw new Error('Passwords do not match');
                    }
                    if (adminPassword.length < 8) {
                        throw new Error('Password must be at least 8 characters');
                    }

                    const response = await fetch(`${API_BASE}/auth/setup-admin`, {
                        method: 'POST',
                        headers: { 'X-API-Key': key, 'Content-Type': 'application/json' },
                        body: JSON.stringify({ email: adminEmail, password: adminPassword })
                    });

                    if (!response.ok) {
                        const data = await response.json();
                        throw new Error(data.error || 'Failed to create admin');
                    }

                    const data = await response.json();
                    authToken = data.token;
                    localStorage.setItem('authToken', authToken);
                } else {
                    // Just use API key
                    apiKey = key;
                    localStorage.setItem('apiKey', apiKey);
                }
            } else {
                // User login
                const email = document.getElementById('login-email').value.trim();
                const password = document.getElementById('login-password').value;

                const response = await fetch(`${API_BASE}/auth/login`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ email, password })
                });

                if (!response.ok) {
                    const data = await response.json();
                    throw new Error(data.error || 'Login failed');
                }

                const data = await response.json();
                authToken = data.token;
                localStorage.setItem('authToken', authToken);
            }

            modal.remove();
            init();
        } catch (error) {
            errorDiv.textContent = error.message;
            errorDiv.classList.remove('hidden');
        }
    });
}

// Show forgot password form
function showForgotPasswordForm() {
    const userFields = document.getElementById('user-login-fields');
    userFields.innerHTML = `
        <h3>Reset Password</h3>
        <label>Email:
            <input type="email" id="reset-email" placeholder="Your email address" required>
        </label>
        <p class="help-text">We'll send you a link to reset your password.</p>
        <button type="button" onclick="requestPasswordReset()" class="primary">Send Reset Link</button>
        <p><a href="#" onclick="location.reload(); return false;">Back to login</a></p>
    `;
}

// Request password reset
async function requestPasswordReset() {
    const email = document.getElementById('reset-email').value.trim();
    if (!email) {
        alert('Please enter your email address');
        return;
    }

    try {
        const response = await fetch(`${API_BASE}/auth/forgot-password`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ email })
        });

        if (response.ok) {
            alert('If an account exists with that email, you will receive a password reset link.');
        } else {
            alert('Failed to send reset email. Please try again.');
        }
    } catch (error) {
        console.error('Password reset error:', error);
        alert('Failed to send reset email. Please try again.');
    }
}

// Load current user info
async function loadCurrentUser() {
    const response = await fetch(`${API_BASE}/auth/me`, {
        headers: getAuthHeaders()
    });

    if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
    }

    currentUser = await response.json();
    updateUserUI();
}

// Update UI with user info
function updateUserUI() {
    // Add user menu to header if not present
    let userMenu = document.getElementById('user-menu');
    if (!userMenu) {
        userMenu = document.createElement('div');
        userMenu.id = 'user-menu';
        userMenu.style.cssText = 'display: flex; align-items: center; gap: 1rem; color: white;';
        document.querySelector('header nav').appendChild(userMenu);
    }

    userMenu.innerHTML = `
        <span>${currentUser.email}</span>
        <span class="user-role">(${currentUser.role})</span>
        <button onclick="logout()" style="background: rgba(255,255,255,0.2);">Logout</button>
    `;

    // Show/hide admin features based on role
    const isAdmin = currentUser.role === 'admin';
    document.querySelectorAll('.admin-only').forEach(el => {
        el.style.display = isAdmin ? '' : 'none';
    });
}

// Logout
async function logout() {
    // Invalidate session on server before clearing local state
    if (authToken) {
        try {
            await fetch(`${API_BASE}/auth/logout`, {
                method: 'POST',
                headers: getAuthHeaders()
            });
        } catch (e) {
            console.log('Server logout failed, continuing with local logout:', e);
        }
    }

    authToken = '';
    apiKey = '';
    currentUser = null;
    localStorage.removeItem('authToken');
    localStorage.removeItem('apiKey');
    location.reload();
}

// Switch tabs
function switchTab(tabName) {
    document.querySelectorAll('.tab-btn').forEach(btn => {
        btn.classList.toggle('active', btn.dataset.tab === tabName);
    });

    document.querySelectorAll('.tab-content').forEach(content => {
        content.classList.toggle('active', content.id === `${tabName}-tab`);
    });

    // Load data for the active tab
    switch (tabName) {
        case 'dashboard':
            loadDashboard();
            break;
        case 'recommendations':
            loadRecommendations();
            break;
        case 'plans':
            loadPlans();
            break;
        case 'history':
            initHistoryDateRange();
            break;
        case 'settings':
            loadGlobalSettings();
            break;
    }
}

// Load dashboard data
async function loadDashboard() {
    try {
        const [summaryData, upcomingData] = await Promise.all([
            fetch(`${API_BASE}/dashboard/summary?provider=${currentProvider}`, { headers: getAuthHeaders() }).then(r => r.json()),
            fetch(`${API_BASE}/dashboard/upcoming`, { headers: getAuthHeaders() }).then(r => r.json())
        ]);

        renderDashboardSummary(summaryData);
        renderSavingsChart(summaryData.by_service || {});
        renderUpcomingPurchases(upcomingData.purchases || []);
    } catch (error) {
        console.error('Failed to load dashboard:', error);
        document.getElementById('summary').innerHTML = `<p class="error">Failed to load dashboard: ${error.message}</p>`;
    }
}

// Render dashboard summary cards
function renderDashboardSummary(data) {
    const formatCurrency = (val) => `$${(val || 0).toLocaleString(undefined, {minimumFractionDigits: 0, maximumFractionDigits: 0})}`;

    document.getElementById('summary').innerHTML = `
        <div class="card">
            <h3>Potential Monthly Savings</h3>
            <p class="value savings">${formatCurrency(data.potential_monthly_savings)}</p>
            <p class="detail">${data.total_recommendations || 0} recommendations</p>
        </div>
        <div class="card">
            <h3>Active Commitments</h3>
            <p class="value">${data.active_commitments || 0}</p>
            <p class="detail">${formatCurrency(data.committed_monthly)}/mo committed</p>
        </div>
        <div class="card">
            <h3>Current Coverage</h3>
            <p class="value">${data.current_coverage || 0}%</p>
            <p class="detail">Target: ${data.target_coverage || 80}%</p>
        </div>
        <div class="card">
            <h3>YTD Savings</h3>
            <p class="value savings">${formatCurrency(data.ytd_savings)}</p>
            <p class="detail">From commitment purchases</p>
        </div>
    `;
}

// Render savings chart by service
function renderSavingsChart(byService) {
    const ctx = document.getElementById('savings-chart');
    if (!ctx) return;

    const labels = Object.keys(byService);
    const potentialSavings = labels.map(s => byService[s].potential_savings || 0);
    const currentSavings = labels.map(s => byService[s].current_savings || 0);

    if (savingsChart) {
        savingsChart.destroy();
    }

    savingsChart = new Chart(ctx, {
        type: 'bar',
        data: {
            labels: labels,
            datasets: [
                {
                    label: 'Potential Savings',
                    data: potentialSavings,
                    backgroundColor: '#fbbc04',
                    borderRadius: 4
                },
                {
                    label: 'Current Savings',
                    data: currentSavings,
                    backgroundColor: '#34a853',
                    borderRadius: 4
                }
            ]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            scales: {
                y: {
                    beginAtZero: true,
                    ticks: {
                        callback: value => '$' + value.toLocaleString()
                    }
                }
            },
            plugins: {
                tooltip: {
                    callbacks: {
                        label: (context) => `${context.dataset.label}: $${context.raw.toLocaleString()}/mo`
                    }
                }
            }
        }
    });
}

// Render upcoming purchases
function renderUpcomingPurchases(purchases) {
    const container = document.getElementById('upcoming-list');

    if (!purchases || purchases.length === 0) {
        container.innerHTML = '<p class="empty">No upcoming scheduled purchases</p>';
        return;
    }

    container.innerHTML = purchases.map(p => {
        const date = new Date(p.scheduled_date);
        return `
            <div class="upcoming-card">
                <div class="upcoming-info">
                    <div class="upcoming-date">
                        <div class="day">${date.getDate()}</div>
                        <div class="month">${date.toLocaleString('default', { month: 'short' })}</div>
                    </div>
                    <div class="upcoming-details">
                        <h4>${p.plan_name}</h4>
                        <p><span class="provider-badge ${p.provider}">${p.provider.toUpperCase()}</span> ${p.service} - Step ${p.step_number} of ${p.total_steps}</p>
                    </div>
                </div>
                <div class="upcoming-savings">
                    <div class="amount">$${(p.estimated_savings || 0).toLocaleString()}</div>
                    <div class="label">Est. monthly savings</div>
                </div>
                <div class="upcoming-actions">
                    <button onclick="viewPurchaseDetails('${p.execution_id}')">View Details</button>
                    <button onclick="cancelPurchase('${p.execution_id}')" class="danger">Cancel</button>
                </div>
            </div>
        `;
    }).join('');
}

// Load recommendations
async function loadRecommendations() {
    try {
        const serviceFilter = document.getElementById('service-filter').value;
        const regionFilter = document.getElementById('region-filter').value;
        const minSavings = document.getElementById('min-savings-filter').value;

        let url = `${API_BASE}/recommendations?provider=${currentProvider}`;
        if (serviceFilter) url += `&service=${serviceFilter}`;
        if (regionFilter) url += `&region=${regionFilter}`;
        if (minSavings) url += `&min_savings=${minSavings}`;

        const response = await fetch(url, { headers: getAuthHeaders() });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);

        const data = await response.json();
        currentRecommendations = data.recommendations || [];
        selectedRecommendations.clear();

        renderRecommendationsSummary(data.summary || {});
        renderRecommendationsList(currentRecommendations);
        populateRegionFilter(data.regions || []);
    } catch (error) {
        console.error('Failed to load recommendations:', error);
        document.getElementById('recommendations-list').innerHTML = `<p class="error">Failed to load recommendations: ${error.message}</p>`;
    }
}

// Render recommendations summary
function renderRecommendationsSummary(summary) {
    const formatCurrency = (val) => `$${(val || 0).toLocaleString(undefined, {minimumFractionDigits: 0, maximumFractionDigits: 0})}`;

    document.getElementById('recommendations-summary').innerHTML = `
        <div class="card">
            <h3>Total Recommendations</h3>
            <p class="value">${summary.total_count || 0}</p>
        </div>
        <div class="card">
            <h3>Potential Monthly Savings</h3>
            <p class="value savings">${formatCurrency(summary.total_monthly_savings)}</p>
        </div>
        <div class="card">
            <h3>Total Upfront Cost</h3>
            <p class="value">${formatCurrency(summary.total_upfront_cost)}</p>
        </div>
        <div class="card">
            <h3>Payback Period</h3>
            <p class="value">${summary.avg_payback_months || 0} months</p>
        </div>
    `;
}

// Render recommendations list
function renderRecommendationsList(recommendations) {
    const container = document.getElementById('recommendations-list');

    if (!recommendations || recommendations.length === 0) {
        container.innerHTML = '<p class="empty">No recommendations found. Try adjusting filters or refreshing.</p>';
        return;
    }

    container.innerHTML = `
        <table>
            <thead>
                <tr>
                    <th class="checkbox-col">
                        <input type="checkbox" onchange="toggleSelectAllRecommendations(this.checked)">
                    </th>
                    <th>Provider</th>
                    <th>Service</th>
                    <th>Resource Type</th>
                    <th>Region</th>
                    <th>Count</th>
                    <th>Term</th>
                    <th>Monthly Savings</th>
                    <th>Upfront Cost</th>
                    <th>Actions</th>
                </tr>
            </thead>
            <tbody>
                ${recommendations.map((rec, index) => {
                    const savingsClass = rec.monthly_savings > 1000 ? 'high-savings' : rec.monthly_savings > 100 ? 'medium-savings' : '';
                    const isSelected = selectedRecommendations.has(index);
                    return `
                    <tr class="${savingsClass} ${isSelected ? 'selected' : ''}">
                        <td class="checkbox-col">
                            <input type="checkbox" ${isSelected ? 'checked' : ''} onchange="toggleRecommendationSelection(${index}, this.checked)">
                        </td>
                        <td><span class="provider-badge ${rec.provider}">${rec.provider.toUpperCase()}</span></td>
                        <td><span class="service-badge">${rec.service}</span></td>
                        <td>${rec.resource_type}${rec.engine ? ` (${rec.engine})` : ''}</td>
                        <td>${rec.region}</td>
                        <td>${rec.count}</td>
                        <td>${rec.term} year</td>
                        <td class="savings">$${(rec.monthly_savings || 0).toLocaleString()}</td>
                        <td>$${(rec.upfront_cost || 0).toLocaleString()}</td>
                        <td>
                            <button onclick="purchaseRecommendation(${index})">Purchase</button>
                        </td>
                    </tr>`;
                }).join('')}
            </tbody>
        </table>
    `;
}

// Populate region filter dropdown
function populateRegionFilter(regions) {
    const select = document.getElementById('region-filter');
    const currentValue = select.value;

    select.innerHTML = '<option value="">All Regions</option>' +
        regions.map(r => `<option value="${r}" ${r === currentValue ? 'selected' : ''}>${r}</option>`).join('');
}

// Toggle recommendation selection
function toggleRecommendationSelection(index, selected) {
    if (selected) {
        selectedRecommendations.add(index);
    } else {
        selectedRecommendations.delete(index);
    }
    renderRecommendationsList(currentRecommendations);
}

// Toggle select all recommendations
function toggleSelectAllRecommendations(selected) {
    if (selected) {
        currentRecommendations.forEach((_, index) => selectedRecommendations.add(index));
    } else {
        selectedRecommendations.clear();
    }
    renderRecommendationsList(currentRecommendations);
}

// Refresh recommendations
async function refreshRecommendations() {
    try {
        await fetch(`${API_BASE}/recommendations/refresh`, {
            method: 'POST',
            headers: getAuthHeaders()
        });
        alert('Recommendation refresh started. This may take a few minutes.');
        setTimeout(loadRecommendations, 5000);
    } catch (error) {
        console.error('Failed to refresh recommendations:', error);
        alert('Failed to start recommendation refresh');
    }
}

// Purchase a single recommendation
function purchaseRecommendation(index) {
    const rec = currentRecommendations[index];
    openPurchaseModal([rec]);
}

// Open create plan modal with selected recommendations
function openCreatePlanModal() {
    if (selectedRecommendations.size === 0) {
        alert('Please select at least one recommendation');
        return;
    }

    document.getElementById('plan-modal-title').textContent = 'Create Purchase Plan';
    document.getElementById('plan-id').value = '';
    document.getElementById('plan-name').value = '';
    document.getElementById('plan-description').value = '';
    document.getElementById('plan-form').reset();
    document.getElementById('plan-modal').classList.remove('hidden');
}

// Open new plan modal
function openNewPlanModal() {
    document.getElementById('plan-modal-title').textContent = 'New Purchase Plan';
    document.getElementById('plan-id').value = '';
    document.getElementById('plan-form').reset();
    document.getElementById('plan-modal').classList.remove('hidden');
}

// Close plan modal
function closePlanModal() {
    document.getElementById('plan-modal').classList.add('hidden');
}

// Save plan
async function savePlan(e) {
    e.preventDefault();

    const planId = document.getElementById('plan-id').value;
    const rampSchedule = document.querySelector('input[name="ramp-schedule"]:checked').value;

    const plan = {
        name: document.getElementById('plan-name').value,
        description: document.getElementById('plan-description').value,
        provider: document.getElementById('plan-provider').value,
        service: document.getElementById('plan-service').value,
        term: parseInt(document.getElementById('plan-term').value),
        payment: document.getElementById('plan-payment').value,
        target_coverage: parseInt(document.getElementById('plan-coverage').value),
        ramp_schedule: rampSchedule,
        auto_purchase: document.getElementById('plan-auto-purchase').checked,
        notification_days_before: parseInt(document.getElementById('plan-notify-days').value),
        enabled: document.getElementById('plan-enabled').checked
    };

    if (rampSchedule === 'custom') {
        plan.custom_step_percent = parseInt(document.getElementById('ramp-step-percent').value);
        plan.custom_interval_days = parseInt(document.getElementById('ramp-interval-days').value);
    }

    // Add selected recommendations if any
    if (selectedRecommendations.size > 0) {
        plan.recommendations = Array.from(selectedRecommendations).map(i => currentRecommendations[i]);
    }

    try {
        const url = planId ? `${API_BASE}/plans/${planId}` : `${API_BASE}/plans`;
        const method = planId ? 'PUT' : 'POST';

        const response = await fetch(url, {
            method,
            headers: getAuthHeaders(),
            body: JSON.stringify(plan)
        });

        if (!response.ok) {
            const data = await response.json();
            throw new Error(data.error || 'Failed to save plan');
        }

        closePlanModal();
        loadPlans();
        alert(planId ? 'Plan updated successfully' : 'Plan created successfully');
    } catch (error) {
        console.error('Failed to save plan:', error);
        alert(`Failed to save plan: ${error.message}`);
    }
}

// Load purchase plans
async function loadPlans() {
    try {
        const response = await fetch(`${API_BASE}/plans`, { headers: getAuthHeaders() });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);

        const data = await response.json();
        renderPlans(data.plans || []);
    } catch (error) {
        console.error('Failed to load plans:', error);
        document.getElementById('plans-list').innerHTML = `<p class="error">Failed to load plans: ${error.message}</p>`;
    }
}

// Render plans list
function renderPlans(plans) {
    const container = document.getElementById('plans-list');

    if (!plans || plans.length === 0) {
        container.innerHTML = '<p class="empty">No purchase plans configured. Create one to automate your commitment purchases.</p>';
        return;
    }

    container.innerHTML = plans.map(plan => {
        const statusClass = plan.enabled ? (plan.auto_purchase ? 'active' : 'paused') : 'disabled';
        const statusLabel = plan.enabled ? (plan.auto_purchase ? 'Active' : 'Manual') : 'Disabled';

        return `
            <div class="plan-card">
                <div class="plan-header">
                    <h3>${plan.name}</h3>
                    <div class="plan-status">
                        <span class="status-badge ${statusClass}">${statusLabel}</span>
                        <label class="toggle-label">
                            <input type="checkbox" ${plan.enabled ? 'checked' : ''} onchange="togglePlan('${plan.id}', this.checked)">
                            <span class="slider"></span>
                        </label>
                    </div>
                </div>
                <div class="plan-body">
                    <div class="plan-details">
                        <div class="plan-detail">
                            <span class="plan-detail-label">Provider</span>
                            <span class="plan-detail-value"><span class="provider-badge ${plan.provider}">${plan.provider.toUpperCase()}</span></span>
                        </div>
                        <div class="plan-detail">
                            <span class="plan-detail-label">Service</span>
                            <span class="plan-detail-value">${plan.service}</span>
                        </div>
                        <div class="plan-detail">
                            <span class="plan-detail-label">Term</span>
                            <span class="plan-detail-value">${plan.term} year</span>
                        </div>
                        <div class="plan-detail">
                            <span class="plan-detail-label">Coverage</span>
                            <span class="plan-detail-value">${plan.target_coverage}%</span>
                        </div>
                        <div class="plan-detail">
                            <span class="plan-detail-label">Ramp Schedule</span>
                            <span class="plan-detail-value">${formatRampSchedule(plan.ramp_schedule)}</span>
                        </div>
                        <div class="plan-detail">
                            <span class="plan-detail-label">Progress</span>
                            <span class="plan-detail-value">${plan.current_step || 0}/${plan.total_steps || 1} steps</span>
                        </div>
                        ${plan.next_execution_date ? `
                        <div class="plan-detail">
                            <span class="plan-detail-label">Next Purchase</span>
                            <span class="plan-detail-value">${new Date(plan.next_execution_date).toLocaleDateString()}</span>
                        </div>
                        ` : ''}
                    </div>
                    <div class="plan-actions">
                        <button onclick="editPlan('${plan.id}')">Edit</button>
                        <button onclick="viewPlanHistory('${plan.id}')" class="secondary">History</button>
                        <button onclick="deletePlan('${plan.id}')" class="danger">Delete</button>
                    </div>
                </div>
            </div>
        `;
    }).join('');
}

// Format ramp schedule for display
function formatRampSchedule(schedule) {
    switch (schedule) {
        case 'immediate': return 'Immediate';
        case 'weekly-25pct': return 'Weekly 25%';
        case 'monthly-10pct': return 'Monthly 10%';
        case 'custom': return 'Custom';
        default: return schedule;
    }
}

// Toggle plan enabled/disabled
async function togglePlan(planId, enabled) {
    try {
        await fetch(`${API_BASE}/plans/${planId}`, {
            method: 'PATCH',
            headers: getAuthHeaders(),
            body: JSON.stringify({ enabled })
        });
        loadPlans();
    } catch (error) {
        console.error('Failed to toggle plan:', error);
        alert('Failed to update plan');
        loadPlans();
    }
}

// Edit plan
async function editPlan(planId) {
    try {
        const response = await fetch(`${API_BASE}/plans/${planId}`, { headers: getAuthHeaders() });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);

        const plan = await response.json();

        document.getElementById('plan-modal-title').textContent = 'Edit Purchase Plan';
        document.getElementById('plan-id').value = plan.id;
        document.getElementById('plan-name').value = plan.name;
        document.getElementById('plan-description').value = plan.description || '';
        document.getElementById('plan-provider').value = plan.provider;
        document.getElementById('plan-service').value = plan.service;
        document.getElementById('plan-term').value = plan.term;
        document.getElementById('plan-payment').value = plan.payment;
        document.getElementById('plan-coverage').value = plan.target_coverage;
        document.getElementById('plan-auto-purchase').checked = plan.auto_purchase;
        document.getElementById('plan-notify-days').value = plan.notification_days_before;
        document.getElementById('plan-enabled').checked = plan.enabled;

        // Set ramp schedule
        document.querySelector(`input[name="ramp-schedule"][value="${plan.ramp_schedule}"]`).checked = true;
        document.getElementById('custom-ramp-config').classList.toggle('hidden', plan.ramp_schedule !== 'custom');

        if (plan.ramp_schedule === 'custom') {
            document.getElementById('ramp-step-percent').value = plan.custom_step_percent || 20;
            document.getElementById('ramp-interval-days').value = plan.custom_interval_days || 7;
        }

        document.getElementById('plan-modal').classList.remove('hidden');
    } catch (error) {
        console.error('Failed to load plan:', error);
        alert('Failed to load plan details');
    }
}

// Delete plan
async function deletePlan(planId) {
    if (!confirm('Are you sure you want to delete this plan? This action cannot be undone.')) {
        return;
    }

    try {
        await fetch(`${API_BASE}/plans/${planId}`, {
            method: 'DELETE',
            headers: getAuthHeaders()
        });
        loadPlans();
    } catch (error) {
        console.error('Failed to delete plan:', error);
        alert('Failed to delete plan');
    }
}

// View plan history
async function viewPlanHistory(planId) {
    switchTab('history');

    // Set date range to cover all history
    const end = new Date();
    const start = new Date();
    start.setFullYear(start.getFullYear() - 1);

    document.getElementById('history-start').value = start.toISOString().split('T')[0];
    document.getElementById('history-end').value = end.toISOString().split('T')[0];

    // Load history and filter by plan
    try {
        const response = await fetch(`${API_BASE}/history?plan_id=${planId}`, { headers: getAuthHeaders() });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);

        const data = await response.json();
        renderHistorySummary(data.summary || {});
        renderHistoryList(data.purchases || []);

        // Show a message indicating filtered view
        const container = document.getElementById('history-list');
        if (container.firstElementChild) {
            const notice = document.createElement('div');
            notice.className = 'filter-notice';
            notice.innerHTML = `<p>Showing history for plan. <a href="#" onclick="loadHistory(); return false;">View all history</a></p>`;
            container.insertBefore(notice, container.firstElementChild);
        }
    } catch (error) {
        console.error('Failed to load plan history:', error);
        document.getElementById('history-list').innerHTML = `<p class="error">Failed to load history: ${error.message}</p>`;
    }
}

// Open purchase modal
function openPurchaseModal(recommendations) {
    const container = document.getElementById('purchase-details');
    const totalSavings = recommendations.reduce((sum, r) => sum + (r.monthly_savings || 0), 0);
    const totalUpfront = recommendations.reduce((sum, r) => sum + (r.upfront_cost || 0), 0);

    container.innerHTML = `
        <div class="form-section">
            <h3>Purchase Summary</h3>
            <p><strong>${recommendations.length}</strong> commitments to purchase</p>
            <p>Estimated Monthly Savings: <strong class="savings">$${totalSavings.toLocaleString()}</strong></p>
            <p>Total Upfront Cost: <strong>$${totalUpfront.toLocaleString()}</strong></p>
        </div>
        <div class="form-section">
            <h3>Commitments</h3>
            <table>
                <thead>
                    <tr><th>Service</th><th>Type</th><th>Region</th><th>Count</th><th>Savings/mo</th></tr>
                </thead>
                <tbody>
                    ${recommendations.map(r => `
                        <tr>
                            <td>${r.service}</td>
                            <td>${r.resource_type}</td>
                            <td>${r.region}</td>
                            <td>${r.count}</td>
                            <td class="savings">$${(r.monthly_savings || 0).toLocaleString()}</td>
                        </tr>
                    `).join('')}
                </tbody>
            </table>
        </div>
    `;

    document.getElementById('purchase-modal').classList.remove('hidden');
}

// Close purchase modal
function closePurchaseModal() {
    document.getElementById('purchase-modal').classList.add('hidden');
}

// Execute purchase
async function executePurchase() {
    if (!confirm('Are you sure you want to execute this purchase? This action will make actual commitment purchases in your cloud account.')) {
        return;
    }

    // Get selected recommendations from the modal
    const selectedRecs = Array.from(selectedRecommendations).map(i => currentRecommendations[i]);

    if (selectedRecs.length === 0) {
        alert('No recommendations selected for purchase');
        return;
    }

    try {
        // Create a purchase request
        const purchaseRequest = {
            recommendations: selectedRecs.map(rec => ({
                provider: rec.provider,
                service: rec.service,
                resource_type: rec.resource_type,
                region: rec.region,
                count: rec.count,
                term: rec.term,
                payment_option: rec.payment_option || 'all-upfront',
                offering_id: rec.offering_id
            }))
        };

        const response = await fetch(`${API_BASE}/purchases/execute`, {
            method: 'POST',
            headers: getAuthHeaders(),
            body: JSON.stringify(purchaseRequest)
        });

        if (!response.ok) {
            const data = await response.json();
            throw new Error(data.error || 'Failed to execute purchase');
        }

        const result = await response.json();

        closePurchaseModal();
        selectedRecommendations.clear();
        loadRecommendations();

        // Show success message with details
        if (result.executed && result.executed.length > 0) {
            alert(`Successfully executed ${result.executed.length} purchase(s).\n\nMonthly savings: $${result.total_monthly_savings?.toLocaleString() || 0}\nUpfront cost: $${result.total_upfront_cost?.toLocaleString() || 0}`);
        } else if (result.status === 'pending_approval') {
            alert('Purchase request submitted. You will receive an email for approval.');
        } else {
            alert('Purchase request submitted successfully.');
        }
    } catch (error) {
        console.error('Failed to execute purchase:', error);
        alert(`Failed to execute purchase: ${error.message}`);
    }
}

// Initialize history date range
function initHistoryDateRange() {
    const end = new Date();
    const start = new Date();
    start.setMonth(start.getMonth() - 3);

    const startInput = document.getElementById('history-start');
    const endInput = document.getElementById('history-end');

    if (!startInput.value) {
        startInput.value = start.toISOString().split('T')[0];
    }
    if (!endInput.value) {
        endInput.value = end.toISOString().split('T')[0];
    }
}

// Load purchase history
async function loadHistory() {
    try {
        const startDate = document.getElementById('history-start').value;
        const endDate = document.getElementById('history-end').value;
        const provider = document.getElementById('history-provider-filter').value;

        let url = `${API_BASE}/history?start=${startDate}&end=${endDate}`;
        if (provider) url += `&provider=${provider}`;

        const response = await fetch(url, { headers: getAuthHeaders() });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);

        const data = await response.json();
        renderHistorySummary(data.summary || {});
        renderHistoryList(data.purchases || []);
    } catch (error) {
        console.error('Failed to load history:', error);
        document.getElementById('history-list').innerHTML = `<p class="error">Failed to load history: ${error.message}</p>`;
    }
}

// Render history summary
function renderHistorySummary(summary) {
    const formatCurrency = (val) => `$${(val || 0).toLocaleString()}`;

    document.getElementById('history-summary').innerHTML = `
        <div class="card">
            <h3>Total Purchases</h3>
            <p class="value">${summary.total_purchases || 0}</p>
        </div>
        <div class="card">
            <h3>Total Upfront Spent</h3>
            <p class="value">${formatCurrency(summary.total_upfront)}</p>
        </div>
        <div class="card">
            <h3>Monthly Savings</h3>
            <p class="value savings">${formatCurrency(summary.total_monthly_savings)}</p>
        </div>
        <div class="card">
            <h3>Annual Savings</h3>
            <p class="value savings">${formatCurrency(summary.total_annual_savings)}</p>
        </div>
    `;
}

// Render history list
function renderHistoryList(purchases) {
    const container = document.getElementById('history-list');

    if (!purchases || purchases.length === 0) {
        container.innerHTML = '<p class="empty">No purchase history found for the selected period.</p>';
        return;
    }

    container.innerHTML = `
        <table>
            <thead>
                <tr>
                    <th>Date</th>
                    <th>Provider</th>
                    <th>Service</th>
                    <th>Type</th>
                    <th>Region</th>
                    <th>Count</th>
                    <th>Term</th>
                    <th>Upfront Cost</th>
                    <th>Monthly Savings</th>
                    <th>Plan</th>
                </tr>
            </thead>
            <tbody>
                ${purchases.map(p => `
                    <tr>
                        <td>${new Date(p.purchase_date).toLocaleDateString()}</td>
                        <td><span class="provider-badge ${p.provider}">${p.provider.toUpperCase()}</span></td>
                        <td>${p.service}</td>
                        <td>${p.resource_type}</td>
                        <td>${p.region}</td>
                        <td>${p.count}</td>
                        <td>${p.term} year</td>
                        <td>$${(p.upfront_cost || 0).toLocaleString()}</td>
                        <td class="savings">$${(p.monthly_savings || 0).toLocaleString()}</td>
                        <td>${p.plan_name || '-'}</td>
                    </tr>
                `).join('')}
            </tbody>
        </table>
    `;
}

// Load global settings
async function loadGlobalSettings() {
    const loadingEl = document.getElementById('settings-loading');
    const formEl = document.getElementById('global-settings-form');
    const errorEl = document.getElementById('settings-error');

    loadingEl.classList.remove('hidden');
    formEl.classList.add('hidden');
    errorEl.classList.add('hidden');

    try {
        const response = await fetch(`${API_BASE}/config`, { headers: getAuthHeaders() });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);

        const data = await response.json();

        // Populate form fields
        if (data.global) {
            document.getElementById('provider-aws').checked = (data.global.enabled_providers || []).includes('aws');
            document.getElementById('provider-azure').checked = (data.global.enabled_providers || []).includes('azure');
            document.getElementById('provider-gcp').checked = (data.global.enabled_providers || []).includes('gcp');
            document.getElementById('setting-notification-email').value = data.global.notification_email || '';
            document.getElementById('setting-auto-collect').checked = data.global.auto_collect !== false;
            document.getElementById('setting-default-term').value = data.global.default_term || 3;
            document.getElementById('setting-default-payment').value = data.global.default_payment || 'all-upfront';
            document.getElementById('setting-default-coverage').value = data.global.default_coverage || 80;
            document.getElementById('setting-notification-days').value = data.global.notification_days_before || 3;
        }

        // Update credential status
        if (data.credentials) {
            const azureStatus = document.getElementById('azure-creds-status');
            const gcpStatus = document.getElementById('gcp-creds-status');

            azureStatus.textContent = data.credentials.azure_configured ? 'Configured' : 'Not Configured';
            azureStatus.classList.toggle('configured', data.credentials.azure_configured);

            gcpStatus.textContent = data.credentials.gcp_configured ? 'Configured' : 'Not Configured';
            gcpStatus.classList.toggle('configured', data.credentials.gcp_configured);
        }

        loadingEl.classList.add('hidden');
        formEl.classList.remove('hidden');
    } catch (error) {
        console.error('Failed to load settings:', error);
        loadingEl.classList.add('hidden');
        errorEl.textContent = `Failed to load settings: ${error.message}`;
        errorEl.classList.remove('hidden');
    }
}

// Save global settings
async function saveGlobalSettings(e) {
    e.preventDefault();

    const enabledProviders = [];
    if (document.getElementById('provider-aws').checked) enabledProviders.push('aws');
    if (document.getElementById('provider-azure').checked) enabledProviders.push('azure');
    if (document.getElementById('provider-gcp').checked) enabledProviders.push('gcp');

    const settings = {
        enabled_providers: enabledProviders,
        notification_email: document.getElementById('setting-notification-email').value,
        auto_collect: document.getElementById('setting-auto-collect').checked,
        default_term: parseInt(document.getElementById('setting-default-term').value),
        default_payment: document.getElementById('setting-default-payment').value,
        default_coverage: parseInt(document.getElementById('setting-default-coverage').value),
        notification_days_before: parseInt(document.getElementById('setting-notification-days').value)
    };

    try {
        const response = await fetch(`${API_BASE}/config`, {
            method: 'PUT',
            headers: getAuthHeaders(),
            body: JSON.stringify(settings)
        });

        if (!response.ok) {
            const data = await response.json();
            throw new Error(data.error || 'Failed to save settings');
        }

        alert('Settings saved successfully');
    } catch (error) {
        console.error('Failed to save settings:', error);
        alert(`Failed to save settings: ${error.message}`);
    }
}

// Reset settings to defaults
function resetSettings() {
    if (!confirm('Are you sure you want to reset all settings to defaults?')) {
        return;
    }

    document.getElementById('provider-aws').checked = true;
    document.getElementById('provider-azure').checked = false;
    document.getElementById('provider-gcp').checked = false;
    document.getElementById('setting-notification-email').value = '';
    document.getElementById('setting-auto-collect').checked = true;
    document.getElementById('setting-default-term').value = '3';
    document.getElementById('setting-default-payment').value = 'all-upfront';
    document.getElementById('setting-default-coverage').value = '80';
    document.getElementById('setting-notification-days').value = '3';
}

// View purchase details
async function viewPurchaseDetails(executionId) {
    try {
        const response = await fetch(`${API_BASE}/purchases/${executionId}`, { headers: getAuthHeaders() });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);

        const purchase = await response.json();

        // Create a detail modal
        const modal = document.createElement('div');
        modal.className = 'modal';
        modal.id = 'purchase-detail-modal';
        modal.innerHTML = `
            <div class="modal-content modal-wide">
                <h2>Purchase Details</h2>
                <div class="purchase-detail-content">
                    <div class="detail-section">
                        <h3>Execution Info</h3>
                        <div class="detail-grid">
                            <div class="detail-item">
                                <span class="label">Execution ID</span>
                                <span class="value">${purchase.execution_id}</span>
                            </div>
                            <div class="detail-item">
                                <span class="label">Status</span>
                                <span class="value status-badge ${purchase.status}">${purchase.status}</span>
                            </div>
                            <div class="detail-item">
                                <span class="label">Scheduled Date</span>
                                <span class="value">${new Date(purchase.scheduled_date).toLocaleString()}</span>
                            </div>
                            <div class="detail-item">
                                <span class="label">Plan</span>
                                <span class="value">${purchase.plan_name || '-'}</span>
                            </div>
                        </div>
                    </div>

                    <div class="detail-section">
                        <h3>Purchase Configuration</h3>
                        <div class="detail-grid">
                            <div class="detail-item">
                                <span class="label">Provider</span>
                                <span class="value"><span class="provider-badge ${purchase.provider}">${(purchase.provider || '').toUpperCase()}</span></span>
                            </div>
                            <div class="detail-item">
                                <span class="label">Service</span>
                                <span class="value">${purchase.service || '-'}</span>
                            </div>
                            <div class="detail-item">
                                <span class="label">Term</span>
                                <span class="value">${purchase.term || '-'} year</span>
                            </div>
                            <div class="detail-item">
                                <span class="label">Payment Option</span>
                                <span class="value">${purchase.payment_option || '-'}</span>
                            </div>
                        </div>
                    </div>

                    <div class="detail-section">
                        <h3>Financial Summary</h3>
                        <div class="detail-grid">
                            <div class="detail-item">
                                <span class="label">Estimated Monthly Savings</span>
                                <span class="value savings">$${(purchase.estimated_monthly_savings || 0).toLocaleString()}</span>
                            </div>
                            <div class="detail-item">
                                <span class="label">Upfront Cost</span>
                                <span class="value">$${(purchase.upfront_cost || 0).toLocaleString()}</span>
                            </div>
                            <div class="detail-item">
                                <span class="label">Coverage Step</span>
                                <span class="value">${purchase.step_number || 1} of ${purchase.total_steps || 1}</span>
                            </div>
                        </div>
                    </div>

                    ${purchase.items && purchase.items.length > 0 ? `
                    <div class="detail-section">
                        <h3>Items (${purchase.items.length})</h3>
                        <table>
                            <thead>
                                <tr>
                                    <th>Type</th>
                                    <th>Region</th>
                                    <th>Count</th>
                                    <th>Savings/mo</th>
                                </tr>
                            </thead>
                            <tbody>
                                ${purchase.items.map(item => `
                                    <tr>
                                        <td>${item.resource_type || '-'}</td>
                                        <td>${item.region || '-'}</td>
                                        <td>${item.count || 1}</td>
                                        <td class="savings">$${(item.monthly_savings || 0).toLocaleString()}</td>
                                    </tr>
                                `).join('')}
                            </tbody>
                        </table>
                    </div>
                    ` : ''}

                    ${purchase.error_message ? `
                    <div class="detail-section error-section">
                        <h3>Error</h3>
                        <p class="error">${purchase.error_message}</p>
                    </div>
                    ` : ''}
                </div>
                <div class="modal-buttons">
                    <button type="button" onclick="document.getElementById('purchase-detail-modal').remove()">Close</button>
                    ${purchase.status === 'pending' || purchase.status === 'scheduled' ? `
                    <button type="button" onclick="cancelPurchase('${purchase.execution_id}'); document.getElementById('purchase-detail-modal').remove();" class="danger">Cancel Purchase</button>
                    ` : ''}
                </div>
            </div>
        `;

        document.body.appendChild(modal);
    } catch (error) {
        console.error('Failed to load purchase details:', error);
        alert(`Failed to load purchase details: ${error.message}`);
    }
}

// Cancel scheduled purchase
async function cancelPurchase(executionId) {
    if (!confirm('Are you sure you want to cancel this scheduled purchase?')) {
        return;
    }

    try {
        await fetch(`${API_BASE}/purchases/cancel/${executionId}`, {
            method: 'POST',
            headers: getAuthHeaders()
        });
        loadDashboard();
        alert('Purchase cancelled successfully');
    } catch (error) {
        console.error('Failed to cancel purchase:', error);
        alert('Failed to cancel purchase');
    }
}

// Initialize on page load
document.addEventListener('DOMContentLoaded', init);
