/**
 * JobHunter // Autonomous AI Career Hunter
 * Minimalist Vercel-Style Client Single Page Application
 */

(function () {
  'use strict';

  // --- State Configuration ---
  const DEFAULT_SERVER = 'http://157.151.254.33:8080';

  const state = {
    serverUrl: localStorage.getItem('jobhunter_server') || DEFAULT_SERVER,
    token: localStorage.getItem('jobhunter_token') || '',
    theme: localStorage.getItem('jobhunter_theme') || 'dark',
    user: null,
    vacancies: [],
    vacanciesTotal: 0,
    vacanciesPage: 1,
    vacanciesLimit: 20,
    searchQuery: '',
    selectedGrade: '',
    selectedSource: '',
    minMatch: 0,
    favorites: [],
    activeModalVacancy: null,
  };

  // --- DOM Elements ---
  const dom = {
    html: document.documentElement,
    serverStatusBadge: document.getElementById('serverStatusBadge'),
    serverStatusText: document.getElementById('serverStatusText'),
    themeToggle: document.getElementById('themeToggle'),
    navUserMeta: document.getElementById('navUserMeta'),
    navStackBadge: document.getElementById('navStackBadge'),
    navStackText: document.getElementById('navStackText'),
    navGradeBadge: document.getElementById('navGradeBadge'),
    navGradeText: document.getElementById('navGradeText'),
    btnRefreshJobs: document.getElementById('btnRefreshJobs'),
    btnAuthModal: document.getElementById('btnAuthModal'),
    authBtnLabel: document.getElementById('authBtnLabel'),

    // Tabs
    tabButtons: document.querySelectorAll('.tab-item'),
    tabPanes: document.querySelectorAll('.tab-pane'),
    tabVacanciesCount: document.getElementById('tabVacanciesCount'),
    tabFavoritesCount: document.getElementById('tabFavoritesCount'),

    // Vacancies tab
    searchInput: document.getElementById('searchInput'),
    btnClearSearch: document.getElementById('btnClearSearch'),
    btnSearchSubmit: document.getElementById('btnSearchSubmit'),
    gradePills: document.getElementById('gradePills'),
    sourceSelect: document.getElementById('sourceSelect'),
    minMatchSelect: document.getElementById('minMatchSelect'),
    vacanciesGrid: document.getElementById('vacanciesGrid'),
    vacanciesLoading: document.getElementById('vacanciesLoading'),
    vacanciesEmpty: document.getElementById('vacanciesEmpty'),
    vacanciesStatsText: document.getElementById('vacanciesStatsText'),
    paginationBottom: document.getElementById('paginationBottom'),
    pageIndicator: document.getElementById('pageIndicator'),
    btnPrevPage: document.getElementById('btnPrevPage'),
    btnNextPage: document.getElementById('btnNextPage'),

    // Favorites tab
    favoritesGrid: document.getElementById('favoritesGrid'),
    favoritesEmpty: document.getElementById('favoritesEmpty'),

    // Profile tab
    stackOptions: document.getElementById('stackOptions'),
    customStackInput: document.getElementById('customStackInput'),
    btnSaveCustomStack: document.getElementById('btnSaveCustomStack'),
    gradeOptions: document.getElementById('gradeOptions'),
    skillsCloud: document.getElementById('skillsCloud'),
    resumeSummaryText: document.getElementById('resumeSummaryText'),
    resumeFileStatusBar: document.getElementById('resumeFileStatusBar'),
    resumeFileName: document.getElementById('resumeFileName'),
    btnDownloadPDF: document.getElementById('btnDownloadPDF'),
    pollingStatusLabel: document.getElementById('pollingStatusLabel'),
    btnTogglePolling: document.getElementById('btnTogglePolling'),

    // Settings tab
    serverUrlInput: document.getElementById('serverUrlInput'),
    tokenInput: document.getElementById('tokenInput'),
    btnSaveConfig: document.getElementById('btnSaveConfig'),
    btnTestConnection: document.getElementById('btnTestConnection'),
    testConnectionResult: document.getElementById('testConnectionResult'),

    // Cover Letter Modal
    modalCoverLetter: document.getElementById('modalCoverLetter'),
    btnCloseClModal: document.getElementById('btnCloseClModal'),
    clModalJobTitle: document.getElementById('clModalJobTitle'),
    clModalCompany: document.getElementById('clModalCompany'),
    clLoading: document.getElementById('clLoading'),
    clContentWrapper: document.getElementById('clContentWrapper'),
    clTextArea: document.getElementById('clTextArea'),
    btnRegenCL: document.getElementById('btnRegenCL'),
    btnCopyCL: document.getElementById('btnCopyCL'),
    btnApplyWithCL: document.getElementById('btnApplyWithCL'),

    // Auth Modal
    modalAuth: document.getElementById('modalAuth'),
    btnCloseAuthModal: document.getElementById('btnCloseAuthModal'),
    loginTokenInput: document.getElementById('loginTokenInput'),
    loginServerInput: document.getElementById('loginServerInput'),
    loginError: document.getElementById('loginError'),
    btnCancelLogin: document.getElementById('btnCancelLogin'),
    btnSubmitLogin: document.getElementById('btnSubmitLogin'),

    // Toast Container
    toastContainer: document.getElementById('toastContainer'),
  };

  // --- Initialize App ---
  function init() {
    parseURLParameters();
    applyTheme(state.theme);
    bindEvents();
    checkHealth();

    if (state.token) {
      loadProfileAndData();
    } else {
      openAuthModal();
    }
  }

  // Parse token & server from URL query params (e.g. ?token=...&server=...)
  function parseURLParameters() {
    const params = new URLSearchParams(window.location.search);
    let changed = false;

    if (params.has('token')) {
      state.token = params.get('token');
      localStorage.setItem('jobhunter_token', state.token);
      changed = true;
    }
    if (params.has('server')) {
      state.serverUrl = params.get('server');
      localStorage.setItem('jobhunter_server', state.serverUrl);
      changed = true;
    }

    if (changed) {
      // Remove query parameters from URL without reloading
      const cleanURL = window.location.origin + window.location.pathname;
      window.history.replaceState({}, document.title, cleanURL);
    }
  }

  // --- API Client ---
  async function apiRequest(endpoint, options = {}) {
    const url = `${state.serverUrl.replace(/\/+$/, '')}${endpoint}`;
    const headers = {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    };

    if (state.token) {
      headers['Authorization'] = `Bearer ${state.token}`;
      headers['X-Session-Token'] = state.token;
    }

    try {
      const resp = await fetch(url, {
        ...options,
        headers,
      });

      if (resp.status === 401) {
        showToast('Сессия истекла или токен недействителен', 'error');
        openAuthModal();
        throw new Error('Unauthorized');
      }

      const data = await resp.json();
      if (!resp.ok) {
        throw new Error(data.message || `HTTP ${resp.status}`);
      }
      return data;
    } catch (err) {
      console.error(`API Error [${endpoint}]:`, err);
      throw err;
    }
  }

  // --- Theme Management ---
  function applyTheme(theme) {
    state.theme = theme;
    localStorage.setItem('jobhunter_theme', theme);
    if (theme === 'dark') {
      dom.html.classList.add('dark');
      dom.html.classList.remove('light');
    } else {
      dom.html.classList.add('light');
      dom.html.classList.remove('dark');
    }
  }

  function toggleTheme() {
    applyTheme(state.theme === 'dark' ? 'light' : 'dark');
  }

  // --- Connectivity & Health ---
  async function checkHealth() {
    try {
      const data = await fetch(`${state.serverUrl.replace(/\/+$/, '')}/health`, { signal: AbortSignal.timeout(5000) });
      if (data.ok) {
        dom.serverStatusBadge.classList.remove('offline');
        const host = new URL(state.serverUrl).host;
        dom.serverStatusText.textContent = host;
        return true;
      }
    } catch (e) {
      dom.serverStatusBadge.classList.add('offline');
      dom.serverStatusText.textContent = 'Офлайн';
    }
    return false;
  }

  // --- Load Profile & Initial Data ---
  async function loadProfileAndData() {
    try {
      const profile = await apiRequest('/api/me');
      state.user = profile;
      updateUserUI();
      dom.authBtnLabel.textContent = `ID: ${profile.user_id}`;

      // Populate Settings Inputs
      dom.serverUrlInput.value = state.serverUrl;
      dom.tokenInput.value = state.token;

      // Load Vacancies & Favorites
      loadVacancies();
      loadFavorites();
    } catch (err) {
      console.warn('Could not load profile:', err);
    }
  }

  function updateUserUI() {
    if (!state.user) return;

    dom.navUserMeta.style.display = 'flex';
    dom.navStackText.textContent = state.user.target_stack || 'Golang';
    dom.navGradeText.textContent = state.user.seniority || 'Junior';

    // Highlight active stack button in Profile tab
    document.querySelectorAll('.btn-stack').forEach(btn => {
      btn.classList.toggle('active', btn.dataset.stack.toLowerCase() === (state.user.target_stack || '').toLowerCase());
    });

    // Highlight active grade button in Profile tab
    document.querySelectorAll('.btn-grade').forEach(btn => {
      btn.classList.toggle('active', btn.dataset.grade.toLowerCase() === (state.user.seniority || '').toLowerCase());
    });

    // Render skills cloud
    if (state.user.skills && state.user.skills.length > 0) {
      dom.skillsCloud.innerHTML = state.user.skills.map(skill => `<span class="skill-tag">${escapeHTML(skill)}</span>`).join('');
    } else {
      dom.skillsCloud.innerHTML = '<span class="empty-hint">Навыки еще не распознаны. Отправьте резюме в Telegram бот.</span>';
    }

    // Render resume summary
    if (state.user.summary) {
      dom.resumeSummaryText.textContent = state.user.summary;
    } else {
      dom.resumeSummaryText.textContent = 'Резюме еще не проанализировано. Загрузите файл резюме в Telegram бота (@SobolnJobBot).';
    }

    // Render PDF resume bar
    if (dom.resumeFileStatusBar) {
      const hasPDF = state.user.pdf_file_path || (state.user.file_name && state.user.file_name.toLowerCase().endsWith('.pdf'));
      if (hasPDF) {
        dom.resumeFileStatusBar.style.display = 'flex';
        dom.resumeFileName.textContent = state.user.file_name || 'Резюме.pdf';
        const pdfUrl = `${state.serverUrl.replace(/\/+$/, '')}/api/me/resume/pdf?token=${encodeURIComponent(state.token)}`;
        dom.btnDownloadPDF.href = pdfUrl;
      } else {
        dom.resumeFileStatusBar.style.display = 'none';
      }
    }

    // Polling status
    if (dom.pollingStatusLabel && dom.btnTogglePolling) {
      const isPolling = !!state.user.is_polling_enabled;
      dom.pollingStatusLabel.innerHTML = `Текущий статус: <b>${isPolling ? 'АКТИВЕН ▶️ (каждые 15 минут)' : 'ВЫКЛЮЧЕН ⏸'}</b>`;
      dom.btnTogglePolling.textContent = isPolling ? '⏸ Отключить постоянный поиск' : '▶️ Включить постоянный поиск';
      dom.btnTogglePolling.className = isPolling ? 'btn btn-secondary' : 'btn btn-primary';
    }
  }

  async function togglePolling() {
    if (!state.user) return;
    const nextState = !state.user.is_polling_enabled;
    try {
      await apiRequest('/api/me/polling', {
        method: 'POST',
        body: JSON.stringify({ enabled: nextState }),
      });
      state.user.is_polling_enabled = nextState;
      updateUserUI();
      showToast(nextState ? 'Постоянный фоновый поиск включен!' : 'Постоянный фоновый поиск остановлен', 'success');
    } catch (err) {
      showToast('Ошибка переключения поиска: ' + err.message, 'error');
    }
  }

  // --- Vacancies View ---
  async function loadVacancies() {
    dom.vacanciesGrid.style.display = 'none';
    dom.vacanciesEmpty.style.display = 'none';
    dom.vacanciesLoading.style.display = 'flex';
    dom.paginationBottom.style.display = 'none';
    dom.vacanciesStatsText.textContent = 'Загрузка вакансий...';

    const params = new URLSearchParams({
      page: state.vacanciesPage,
      limit: state.vacanciesLimit,
    });

    if (state.searchQuery) params.set('search', state.searchQuery);
    if (state.selectedGrade) params.set('grade', state.selectedGrade);
    if (state.selectedSource) params.set('source', state.selectedSource);
    if (state.minMatch > 0) params.set('min_match', state.minMatch);

    try {
      const data = await apiRequest(`/api/vacancies?${params.toString()}`);
      state.vacancies = data.vacancies || [];
      state.vacanciesTotal = data.total || 0;

      dom.tabVacanciesCount.textContent = state.vacanciesTotal;
      renderVacanciesList();
    } catch (err) {
      dom.vacanciesLoading.style.display = 'none';
      dom.vacanciesEmpty.style.display = 'block';
      dom.vacanciesStatsText.textContent = 'Ошибка загрузки вакансий';
      showToast('Не удалось загрузить вакансии: ' + err.message, 'error');
    }
  }

  function renderVacanciesList() {
    dom.vacanciesLoading.style.display = 'none';

    if (state.vacancies.length === 0) {
      dom.vacanciesGrid.style.display = 'none';
      dom.vacanciesEmpty.style.display = 'block';
      dom.vacanciesStatsText.textContent = 'Найдено вакансий: 0';
      return;
    }

    dom.vacanciesEmpty.style.display = 'none';
    dom.vacanciesGrid.style.display = 'grid';

    const totalPages = Math.ceil(state.vacanciesTotal / state.vacanciesLimit) || 1;
    dom.vacanciesStatsText.textContent = `Показано ${state.vacancies.length} из ${state.vacanciesTotal} вакансий`;

    dom.pageIndicator.textContent = `Страница ${state.vacanciesPage} из ${totalPages}`;
    dom.btnPrevPage.disabled = state.vacanciesPage <= 1;
    dom.btnNextPage.disabled = state.vacanciesPage >= totalPages;
    dom.paginationBottom.style.display = totalPages > 1 ? 'flex' : 'none';

    dom.vacanciesGrid.innerHTML = state.vacancies.map(v => createVacancyCardHTML(v)).join('');
  }

  function createVacancyCardHTML(v) {
    const isFav = v.status === 'favorite';
    const matchClass = v.match_pct >= 75 ? 'match-high' : (v.match_pct >= 50 ? 'match-mid' : 'match-low');
    const gradeClass = `grade-${(v.expected_grade || 'Junior').toLowerCase()}`;

    const analysisSnippet = v.match_analysis
      ? `<div class="card-analysis">${escapeHTML(v.match_analysis)}</div>`
      : '';

    return `
      <div class="vacancy-card" data-id="${escapeHTML(v.id)}">
        <div class="card-top">
          <div class="card-title-group">
            <a href="${escapeHTML(v.url)}" target="_blank" rel="noopener noreferrer" class="card-title-link">
              ${escapeHTML(v.title)} ↗
            </a>
            <div class="card-subtitle">
              <span class="card-company">${escapeHTML(v.company || 'Компания не указана')}</span>
              ${v.location ? `<span>•</span> <span class="card-location">${escapeHTML(v.location)}</span>` : ''}
            </div>
          </div>
          <div class="card-badges">
            <span class="badge-grade ${gradeClass}">${escapeHTML(v.expected_grade || 'Junior')}</span>
            <span class="badge-match ${matchClass}">${v.match_pct}% match</span>
          </div>
        </div>

        <div class="card-details">
          ${v.salary ? `<span class="card-salary">💰 ${escapeHTML(v.salary)}</span>` : ''}
          <span class="badge-source">${escapeHTML(v.source)}</span>
        </div>

        ${analysisSnippet}

        <div class="card-actions">
          <div class="card-actions-left">
            <button class="btn btn-secondary btn-sm btn-star ${isFav ? 'favorited' : ''}" onclick="window.jobhunter.toggleFavorite('${escapeHTML(v.id)}')">
              ${isFav ? '⭐ В избранном' : '☆ В избранное'}
            </button>
            <button class="btn btn-secondary btn-sm" onclick="window.jobhunter.openCoverLetter('${escapeHTML(v.id)}')">
              📝 Сопроводительное
            </button>
          </div>
          <div class="card-actions-right">
            <button class="btn btn-primary btn-sm" onclick="window.jobhunter.applyToVacancy('${escapeHTML(v.id)}')">
              🚀 Откликнуться
            </button>
          </div>
        </div>
      </div>
    `;
  }

  // --- Favorites View ---
  async function loadFavorites() {
    try {
      const data = await apiRequest('/api/favorites');
      state.favorites = data.favorites || [];
      dom.tabFavoritesCount.textContent = state.favorites.length;
      renderFavoritesList();
    } catch (err) {
      console.warn('Could not load favorites:', err);
    }
  }

  function renderFavoritesList() {
    if (state.favorites.length === 0) {
      dom.favoritesGrid.style.display = 'none';
      dom.favoritesEmpty.style.display = 'block';
      return;
    }

    dom.favoritesEmpty.style.display = 'none';
    dom.favoritesGrid.style.display = 'grid';
    dom.favoritesGrid.innerHTML = state.favorites.map(v => createVacancyCardHTML(v)).join('');
  }

  // --- Toggle Favorite ---
  async function toggleFavorite(vacancyID) {
    try {
      const res = await apiRequest('/api/favorites/toggle', {
        method: 'POST',
        body: JSON.stringify({ vacancy_id: vacancyID }),
      });

      // Update in vacancies list
      const item = state.vacancies.find(v => v.id === vacancyID);
      if (item) {
        item.status = res.is_favorite ? 'favorite' : 'new';
        renderVacanciesList();
      }

      // Reload favorites tab
      loadFavorites();

      showToast(res.is_favorite ? 'Добавлено в избранное ⭐' : 'Удалено из избранного', 'success');
    } catch (err) {
      showToast('Ошибка обновления избранного: ' + err.message, 'error');
    }
  }

  // --- Cover Letter Modal ---
  async function openCoverLetter(vacancyID, forceRegenerate = false) {
    let vac = state.vacancies.find(v => v.id === vacancyID) || state.favorites.find(v => v.id === vacancyID);
    if (!vac) {
      vac = { id: vacancyID, title: 'Вакансия', company: '' };
    }

    state.activeModalVacancy = vac;
    dom.clModalJobTitle.textContent = vac.title;
    dom.clModalCompany.textContent = vac.company ? `@ ${vac.company}` : '';

    dom.modalCoverLetter.style.display = 'flex';
    dom.clContentWrapper.style.display = 'none';
    dom.clLoading.style.display = 'flex';

    try {
      const res = await apiRequest('/api/cover-letter', {
        method: 'POST',
        body: JSON.stringify({
          vacancy_id: vacancyID,
          regenerate: forceRegenerate,
        }),
      });

      dom.clLoading.style.display = 'none';
      dom.clContentWrapper.style.display = 'block';
      dom.clTextArea.value = res.cover_letter || '';
    } catch (err) {
      dom.clLoading.style.display = 'none';
      dom.clContentWrapper.style.display = 'block';
      dom.clTextArea.value = 'Ошибка генерации сопроводительного письма: ' + err.message;
    }
  }

  function copyCoverLetter() {
    const text = dom.clTextArea.value;
    if (!text) return;
    navigator.clipboard.writeText(text).then(() => {
      showToast('Сопроводительное скопировано в буфер обмена! 📋', 'success');
    }).catch(() => {
      dom.clTextArea.select();
      document.execCommand('copy');
      showToast('Скопировано в буфер!', 'success');
    });
  }

  // --- Apply to Vacancy ---
  async function applyToVacancy(vacancyID) {
    const vac = state.vacancies.find(v => v.id === vacancyID) || state.favorites.find(v => v.id === vacancyID);
    showToast('Отправка отклика через автономный браузер...', 'normal');

    try {
      const res = await apiRequest('/api/apply', {
        method: 'POST',
        body: JSON.stringify({ vacancy_id: vacancyID }),
      });

      showToast(res.message || 'Отклик успешно зарегистрирован!', 'success');
      if (dom.modalCoverLetter.style.display === 'flex') {
        dom.modalCoverLetter.style.display = 'none';
      }
    } catch (err) {
      if (vac && vac.url) {
        window.open(vac.url, '_blank');
      }
      showToast('Открыта страница вакансии для прямого отклика', 'normal');
    }
  }

  // --- Refresh Sweeper ---
  async function triggerRefreshJobs() {
    const btn = dom.btnRefreshJobs;
    btn.disabled = true;
    btn.classList.add('loading');
    showToast('Запуск сбора свежих вакансий со всех площадок...', 'normal');

    try {
      const res = await apiRequest('/api/vacancies/refresh', { method: 'POST' });
      showToast(`Сбор завершен! Новых вакансий: ${res.newly_found || 0}`, 'success');
      state.vacanciesPage = 1;
      loadVacancies();
    } catch (err) {
      showToast('Ошибка обновления: ' + err.message, 'error');
    } finally {
      btn.disabled = false;
      btn.classList.remove('loading');
    }
  }

  // --- Auth Modal & Session Management ---
  function openAuthModal() {
    dom.loginTokenInput.value = state.token;
    dom.loginServerInput.value = state.serverUrl;
    dom.loginError.style.display = 'none';
    dom.modalAuth.style.display = 'flex';
  }

  async function submitAuth() {
    const token = dom.loginTokenInput.value.trim();
    const server = dom.loginServerInput.value.trim() || DEFAULT_SERVER;

    if (!token) {
      dom.loginError.textContent = 'Пожалуйста, введите токен сессии';
      dom.loginError.style.display = 'block';
      return;
    }

    state.serverUrl = server;
    state.token = token;
    localStorage.setItem('jobhunter_server', server);
    localStorage.setItem('jobhunter_token', token);

    dom.btnSubmitLogin.disabled = true;
    dom.loginError.style.display = 'none';

    try {
      await loadProfileAndData();
      dom.modalAuth.style.display = 'none';
      showToast('Авторизация успешна! Добро пожаловать.', 'success');
    } catch (err) {
      dom.loginError.textContent = 'Ошибка проверки токена: ' + err.message;
      dom.loginError.style.display = 'block';
    } finally {
      dom.btnSubmitLogin.disabled = false;
    }
  }

  // --- Profile Updates (Stack & Grade) ---
  async function updateTargetStack(stack) {
    try {
      await apiRequest('/api/me/stack', {
        method: 'POST',
        body: JSON.stringify({ stack }),
      });
      state.user.target_stack = stack;
      updateUserUI();
      showToast(`Стек изменен на: ${stack}. Запустите «Обновить» для нового поиска.`, 'success');
    } catch (err) {
      showToast('Ошибка сохранения стека: ' + err.message, 'error');
    }
  }

  async function updateSeniorityGrade(grade) {
    try {
      await apiRequest('/api/me/grade', {
        method: 'POST',
        body: JSON.stringify({ grade }),
      });
      state.user.seniority = grade;
      updateUserUI();
      showToast(`Грейд установлен: ${grade}`, 'success');
    } catch (err) {
      showToast('Ошибка обновления грейда: ' + err.message, 'error');
    }
  }

  // --- Event Bindings ---
  function bindEvents() {
    // Theme Switch
    dom.themeToggle.addEventListener('click', toggleTheme);

    // Refresh sweep
    dom.btnRefreshJobs.addEventListener('click', triggerRefreshJobs);

    // Auth modal triggers
    dom.btnAuthModal.addEventListener('click', openAuthModal);
    dom.btnCloseAuthModal.addEventListener('click', () => dom.modalAuth.style.display = 'none');
    dom.btnCancelLogin.addEventListener('click', () => dom.modalAuth.style.display = 'none');
    dom.btnSubmitLogin.addEventListener('click', submitAuth);

    // Tabs switching
    dom.tabButtons.forEach(btn => {
      btn.addEventListener('click', () => {
        const tab = btn.dataset.tab;
        dom.tabButtons.forEach(b => b.classList.toggle('active', b === btn));
        dom.tabPanes.forEach(p => p.classList.toggle('active', p.id === `tab-${tab}`));

        if (tab === 'favorites') loadFavorites();
        if (tab === 'vacancies') loadVacancies();
        if (tab === 'profile') loadProfileAndData();
      });
    });

    // Search events
    dom.btnSearchSubmit.addEventListener('click', () => {
      state.searchQuery = dom.searchInput.value.trim();
      state.vacanciesPage = 1;
      loadVacancies();
    });

    dom.searchInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        state.searchQuery = dom.searchInput.value.trim();
        state.vacanciesPage = 1;
        loadVacancies();
      }
    });

    dom.searchInput.addEventListener('input', () => {
      dom.btnClearSearch.style.display = dom.searchInput.value ? 'block' : 'none';
    });

    dom.btnClearSearch.addEventListener('click', () => {
      dom.searchInput.value = '';
      dom.btnClearSearch.style.display = 'none';
      state.searchQuery = '';
      state.vacanciesPage = 1;
      loadVacancies();
    });

    // Grade pill clicks
    dom.gradePills.addEventListener('click', (e) => {
      const pill = e.target.closest('.pill');
      if (!pill) return;
      dom.gradePills.querySelectorAll('.pill').forEach(p => p.classList.remove('active'));
      pill.classList.add('active');
      state.selectedGrade = pill.dataset.grade || '';
      state.vacanciesPage = 1;
      loadVacancies();
    });

    // Dropdowns
    dom.sourceSelect.addEventListener('change', () => {
      state.selectedSource = dom.sourceSelect.value;
      state.vacanciesPage = 1;
      loadVacancies();
    });

    dom.minMatchSelect.addEventListener('change', () => {
      state.minMatch = parseInt(dom.minMatchSelect.value, 10) || 0;
      state.vacanciesPage = 1;
      loadVacancies();
    });

    // Pagination
    dom.btnPrevPage.addEventListener('click', () => {
      if (state.vacanciesPage > 1) {
        state.vacanciesPage--;
        loadVacancies();
        window.scrollTo({ top: 0, behavior: 'smooth' });
      }
    });

    dom.btnNextPage.addEventListener('click', () => {
      const totalPages = Math.ceil(state.vacanciesTotal / state.vacanciesLimit) || 1;
      if (state.vacanciesPage < totalPages) {
        state.vacanciesPage++;
        loadVacancies();
        window.scrollTo({ top: 0, behavior: 'smooth' });
      }
    });

    // Profile Tab: Stack Selector Buttons
    dom.stackOptions.addEventListener('click', (e) => {
      const btn = e.target.closest('.btn-stack');
      if (!btn) return;
      updateTargetStack(btn.dataset.stack);
    });

    // Profile Tab: Custom Stack
    dom.btnSaveCustomStack.addEventListener('click', () => {
      const stack = dom.customStackInput.value.trim();
      if (stack) {
        updateTargetStack(stack);
        dom.customStackInput.value = '';
      }
    });

    // Profile Tab: Grade Selector
    dom.gradeOptions.addEventListener('click', (e) => {
      const btn = e.target.closest('.btn-grade');
      if (!btn) return;
      updateSeniorityGrade(btn.dataset.grade);
    });

    // Profile Tab: Polling Toggle
    if (dom.btnTogglePolling) {
      dom.btnTogglePolling.addEventListener('click', togglePolling);
    }

    // Settings Tab: Save API Config
    dom.btnSaveConfig.addEventListener('click', () => {
      state.serverUrl = dom.serverUrlInput.value.trim() || DEFAULT_SERVER;
      state.token = dom.tokenInput.value.trim();
      localStorage.setItem('jobhunter_server', state.serverUrl);
      localStorage.setItem('jobhunter_token', state.token);
      showToast('Настройки подключения сохранены!', 'success');
      checkHealth();
      loadProfileAndData();
    });

    // Settings Tab: Test Connection
    dom.btnTestConnection.addEventListener('click', async () => {
      dom.testConnectionResult.textContent = 'Проверка...';
      const ok = await checkHealth();
      dom.testConnectionResult.textContent = ok ? '✅ Сервер доступен и отвечает!' : '❌ Связь не установлена';
      dom.testConnectionResult.style.color = ok ? '#22c55e' : '#ef4444';
    });

    // Cover Letter Modal Events
    dom.btnCloseClModal.addEventListener('click', () => dom.modalCoverLetter.style.display = 'none');
    dom.btnCopyCL.addEventListener('click', copyCoverLetter);
    dom.btnRegenCL.addEventListener('click', () => {
      if (state.activeModalVacancy) {
        openCoverLetter(state.activeModalVacancy.id, true);
      }
    });
    dom.btnApplyWithCL.addEventListener('click', () => {
      if (state.activeModalVacancy) {
        applyToVacancy(state.activeModalVacancy.id);
      }
    });
  }

  // --- Toast Notifications ---
  function showToast(message, type = 'normal') {
    const toast = document.createElement('div');
    toast.className = `toast toast-${type}`;
    toast.textContent = message;
    dom.toastContainer.appendChild(toast);

    setTimeout(() => {
      toast.style.opacity = '0';
      toast.style.transition = 'opacity 0.3s ease';
      setTimeout(() => toast.remove(), 300);
    }, 3500);
  }

  // Utility: HTML Escaping
  function escapeHTML(str) {
    if (!str) return '';
    return str
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }

  // Expose methods to global scope for HTML inline handlers
  window.jobhunter = {
    toggleFavorite,
    openCoverLetter,
    applyToVacancy,
  };

  // Start Application
  document.addEventListener('DOMContentLoaded', init);
})();
