import { createApp, nextTick } from './frontend-assets/vue.esm-browser.prod.js';
import { calculate, catalogs, formatDuration, networkTime, parseDuration } from './incident-model.mjs';

const periodQuery = period => new URLSearchParams(period).toString();
const dateLabel = value => value ? value.slice(0,10).split('-').reverse().join('.') : '';
function reportDateOffset(date, days) {
  const value = new Date(date + 'T00:00:00Z');
  value.setUTCDate(value.getUTCDate() + days);
  return value.toISOString().slice(0, 10);
}
function currentPage() {
  const page = location.hash.slice(1);
  return ['reports', 'engineers', 'reference', 'shifts', 'solutions', 'schedule'].includes(page) ? page : 'incidents';
}

function emptyAssignment() {
  return { engineerId: 0, operator: '', start: '', end: '', note: '' };
}

function emptySolutionVariant() {
  return { condition: '', checks: '', repair: '', verification: '' };
}

function emptySolution() {
  return { title: '', symptoms: '', station: '', repairedAt: networkTime().slice(0,16), variants: [emptySolutionVariant()] };
}

function emptyShift() {
  return { engineerId: 0, operator: '', start: '', end: networkTime().slice(0, 16), summary: '', openIncidents: '', handover: '' };
}

function crmLink(value) {
  if (!/^https?:\/\//i.test(value)) return '';
  try { return new URL(value).href; } catch { return ''; }
}

async function request(path, method = 'GET', data) {
  const headers = method === 'GET' ? {} : { 'Content-Type': 'application/json', 'X-Svyazi-Net-Request': '1' };
  const response = await fetch('/api/' + path, { method, headers, body: JSON.stringify(data), cache: 'no-store' });
  const result = await response.json();
  if (!response.ok) {
    const error = new Error(result.error);
    error.status = response.status;
    throw error;
  }
  return result;
}

const IncidentTable = {
  template: '#incident-table-template',
  props: { incidents: Array, report: Boolean, editable: Boolean, personal: Boolean },
  emits: ['open'],
  data: () => ({ page: 1, filters: { direction: '', center: '', category: '', network: '', type: '' }, order: 'newest', filterLabels: { direction: 'Направление', center: 'ТЦ', category: 'Категория', network: 'Тип сети', type: 'Тип аварии' } }),
  computed: {
    filteredRows() {
      return this.incidents.filter(row => Object.entries(this.filters).every(([key, value]) => !value || row[key] === value))
        .slice().sort((a, b) => this.order === 'category'
          ? ((Number(a.category) || 99) - (Number(b.category) || 99) || b.start.localeCompare(a.start) || b.id - a.id)
          : (this.order === 'oldest' ? 1 : -1) * (a.start.localeCompare(b.start) || a.id - b.id));
    },
    pageCount() { return Math.max(1, Math.ceil(this.filteredRows.length / 25)); },
    visibleRows() { return this.filteredRows.slice((this.page - 1) * 25, this.page * 25); },
    visibleRange() {
      const first = (this.page - 1) * 25 + 1;
      return this.filteredRows.length ? `${first}–${first + this.visibleRows.length - 1} из ${this.filteredRows.length}` : '0 записей';
    },
  },
  watch: { filteredRows() {
    this.page = 1;
    if (this.$refs.tableRegion) this.$refs.tableRegion.scrollTop = 0;
  } },
  methods: {
    dateLabel, crmLink,
    filterOptions(key) { return [...new Set(this.incidents.map(row => row[key]).filter(Boolean))].sort(); },
    changePage(offset) {
      this.page += offset;
      this.$refs.tableRegion.scrollTop = 0;
    },
  },
};

createApp({
  components: { IncidentTable },
  data() {
    return {
      catalogs, journal: [], report: [], period: { from: '', to: '' }, shownPeriod: null,
      today: '', role: null, roleLabel: '', login: '', password: '',
      personalEngineer: false, engineers: [], newEngineer: { center: '' }, engineerError: '', addingEngineer: false,
      referenceTab: 'stations', stations: [], stationQuery: '', stationCenter: '', stationLimit: 20,
      stationLoading: false, stationError: '', stationRequest: 0,
      shift: emptyShift(), shiftReports: [], shiftLoading: false, shiftSaving: false, shiftError: '', shiftSession: 0,
      solutionDraft: emptySolution(), solutions: [], solutionQuery: '', solutionLimit: 20, solutionSession: 0,
      solutionLoading: false, solutionSaving: false, solutionError: '', solutionFormOpen: false,
      schedule: [], scheduleDraft: null, scheduleMonth: networkTime().slice(0, 7), scheduleLoading: false,
      scheduleSaving: false, scheduleError: '', scheduleSession: 0, scheduleCenter: '',
      initializing: true, loading: false, authenticating: false, error: '', formError: '', saving: false,
      reportError: '', exportError: '', exporting: false, exportFormat: 'xlsx', savedMessage: '',
      page: currentPage(), theme: document.documentElement.dataset.theme || 'dark',
      draft: {}, drawerMode: 'new', drawerVisible: false, now: networkTime(),
    };
  },
  computed: {
    canSeeShifts() { return this.role === 'duty' || this.role === 'admin'; },
    scheduleCenters() { return [...new Set(this.schedule.map(shift => shift.center).filter(Boolean))].sort(); },
    centerSchedule() { return this.schedule.filter(shift => !this.scheduleCenter || shift.center === this.scheduleCenter); },
    activeDuty() { return this.centerSchedule.filter(shift => shift.start <= this.now && this.now < shift.end); },
    nextDuty() { return this.centerSchedule.find(shift => shift.start > this.now); },
    monthlyDuty() {
      if (!this.scheduleMonth) return this.centerSchedule;
      const monthStart = this.scheduleMonth + '-01T00:00:00';
      return this.centerSchedule.filter(shift => shift.start.slice(0, 7) <= this.scheduleMonth && shift.end > monthStart);
    },
    filteredSolutions() {
      const terms = this.solutionQuery.trim().toLowerCase().split(/\s+/).filter(Boolean);
      return this.solutions.filter(entry => {
        const text = [entry.title, entry.symptoms, entry.author, entry.center, entry.station, entry.repairedAt,
          ...entry.variants.map(variant => Object.values(variant).join(' '))].join(' ').toLowerCase();
        return terms.every(term => text.includes(term));
      });
    },
    filteredStations() {
      const terms = this.stationQuery.toLowerCase().trim().split(/\s+/).filter(Boolean);
      return this.stations.filter(station => {
        if (this.stationCenter && this.stationCenter !== station.center) return false;
        const label = [station.element, station.center, station.direction, station.network].join(' ').toLowerCase();
        return terms.every(term => label.includes(term));
      });
    },
    stationCenters() {
      return [...new Set(this.stations.map(station => station.center).filter(Boolean))].sort();
    },
    themeAction() {
      return this.theme === 'dark' ? 'Включить светлую тему' : 'Включить тёмную тему';
    },
    availableEngineers() { return this.engineers.filter(person => person.center === this.draft.center); },
    metrics() {
      const active = this.journal.filter(event => !event.end);
      return {
        active: active.length,
        critical: active.filter(event => event.category === '1').length,
        stations: active.reduce((sum, event) => sum + (Number(event.stations) || 0), 0),
        downtime: this.journal.reduce((sum, event) => sum + (event.total || 0), 0),
        closed: this.journal.filter(event => event.end && event.end.slice(0,10) === this.today).length,
      };
    },
    drawerTitle() {
      if (this.drawerMode === 'new') return 'Новая авария';
      return this.draft.ticket || 'Авария № ' + this.draft.id;
    },
    drawerSubtitle() {
      if (this.drawerMode === 'new') return 'Карточка события в журнале аварий';
      if (this.drawerMode === 'edit') return 'Редактирование события';
      return this.draft.summary;
    },
    calculated() {
      const result = calculate({
        ...this.draft, end: this.draft.ongoing ? '' : this.draft.end,
        outage: parseDuration(this.draft.outage),
      }, this.now);
      if (!this.draft.ongoing && !this.draft.end) result.duration = null;
      return result;
    },
    reportPeriodLabel() {
      if (!this.shownPeriod) return '';
      const { from, to } = this.shownPeriod;
      return from === to ? dateLabel(from) : dateLabel(from) + ' — ' + dateLabel(to);
    },
    periodChanged() {
      return Boolean(this.shownPeriod) && (this.period.from !== this.shownPeriod.from || this.period.to !== this.shownPeriod.to);
    },
    canExport() {
      return this.report.length > 0 && !this.periodChanged && !this.loading && !this.exporting;
    },
  },
  async mounted() {
    document.getElementById('startup-note').hidden = true;
    window.addEventListener('hashchange', this.syncPage);
    this.clock = setInterval(() => { this.now = networkTime(); }, 1000);
    this.sessionClock = setInterval(async () => {
      if (!this.role) return;
      try {
        const session = await request('session');
        if (session.role !== this.role) await this.useSession(session);
      } catch (error) { this.showError(error); }
    }, 15000);
    try {
      await this.useSession(await request('session'));
    } catch (error) {
      this.showError(error);
    } finally {
      this.initializing = false;
    }
  },
  beforeUnmount() {
    window.removeEventListener('hashchange', this.syncPage);
    clearInterval(this.clock);
    clearInterval(this.sessionClock);
    clearTimeout(this.noticeTimer);
  },
  methods: {
    dateLabel, formatDuration,
    clearSuccess() {
      clearTimeout(this.noticeTimer);
      this.savedMessage = '';
    },
    showSuccess(message, page) {
      if (!this.role || currentPage() !== page) return;
      this.clearSuccess();
      this.savedMessage = message;
      this.noticeTimer = setTimeout(this.clearSuccess, 4000);
    },
    toggleTheme() {
      this.theme = this.theme === 'dark' ? 'light' : 'dark';
      document.documentElement.dataset.theme = this.theme;
      try { localStorage.setItem('svyazi.net-theme', this.theme); } catch {}
    },
    syncPage() {
      this.clearSuccess();
      this.page = currentPage();
      this.closeIncident();
      this.checkPageAccess();
      if (this.page === 'reference' && this.role) this.loadStations();
      if (this.page === 'shifts' && this.canSeeShifts) this.loadShiftReports();
      if (this.page === 'solutions' && this.role) this.loadSolutions();
      if (this.page === 'schedule' && this.role) this.loadSchedule();
      window.scrollTo(0, 0);
    },
    checkPageAccess() {
      if (!this.role) return;
      if ((this.page === 'shifts' && !this.canSeeShifts) || (this.page === 'engineers' && this.role !== 'admin')) {
        this.page = 'incidents';
        location.hash = '#incidents';
      }
    },
    showError(error) {
      this.error = error.message || 'Не удалось выполнить запрос.';
      if (error.status === 401 && this.role) {
        this.clearSuccess();
        this.role = null;
        this.resetStations();
        this.resetShifts();
        this.resetSolutions();
        this.resetSchedule();
        this.closeIncident();
      }
    },
    async useSession(session) {
      this.role = session.role;
      this.resetStations();
      this.resetShifts();
      this.resetSolutions();
      this.resetSchedule();
      this.roleLabel = session.roleLabel;
      this.personalEngineer = session.engineer;
      this.engineers = [];
      if (!this.role) return;
      if (this.page === 'reference') this.loadStations();
      this.checkPageAccess();
      if (this.page === 'shifts') this.loadShiftReports();
      if (this.page === 'solutions') this.loadSolutions();
      if (this.page === 'schedule') this.loadSchedule();
      this.clearSuccess();
      await this.loadJournal();
      this.period = { from: this.latest, to: this.latest };
      await this.loadReport();
      if (this.role === 'admin') this.engineers = await request('engineers');
    },
    async signIn() {
      this.error = '';
      this.authenticating = true;
      try {
        const session = await request('login', 'POST', { login: this.login, password: this.password });
        this.password = '';
        await this.useSession(session);
      } catch (error) {
        this.showError(error);
      } finally {
        this.authenticating = false;
      }
    },
    async signOut() {
      try {
        await request('logout', 'POST', {});
        this.clearSuccess();
        this.role = null;
        this.resetStations();
        this.resetShifts();
        this.resetSolutions();
        this.resetSchedule();
        this.error = '';
        this.journal = [];
        this.report = [];
        this.shownPeriod = null;
      } catch (error) { this.showError(error); }
    },
    async loadJournal() {
      const { events, today, latest } = await request('journal');
      this.journal = events;
      this.today = today;
      this.latest = latest;
    },
    resetShifts() {
      this.shiftSession++;
      this.shiftReports = [];
      this.shiftLoading = false;
      this.shiftSaving = false;
      this.shiftError = '';
      this.shift = emptyShift();
    },
    async loadShiftReports() {
      if (this.shiftLoading || this.shiftSaving) return;
      const session = this.shiftSession;
      this.shiftLoading = true;
      this.shiftError = '';
      try {
        const reports = await request('shifts');
        if (session === this.shiftSession) this.shiftReports = reports;
      } catch (error) {
        if (session === this.shiftSession) this.showShiftError(error);
      } finally {
        if (session === this.shiftSession) this.shiftLoading = false;
      }
    },
    async submitShift() {
      if (this.shiftSaving || this.shiftLoading || this.role !== 'duty') return;
      const session = this.shiftSession;
      this.shiftSaving = true;
      this.shiftError = '';
      this.clearSuccess();
      try {
        await request('shifts', 'POST', this.shift);
        if (session !== this.shiftSession) return;
        this.showSuccess('Отчёт смены сохранён', 'shifts');
        this.shift = { ...emptyShift(), operator: this.shift.operator };
      } catch (error) {
        if (session === this.shiftSession) this.showShiftError(error);
        return;
      } finally {
        if (session === this.shiftSession) this.shiftSaving = false;
      }
      await this.loadShiftReports();
    },
    showShiftError(error) {
      if (error.status === 401) this.showError(error);
      else this.shiftError = error.message;
    },
    shiftTime(value) { return `${dateLabel(value)} ${value.slice(11, 16)}`; },
    resetSchedule() {
      this.scheduleSession++;
      this.schedule = [];
      this.scheduleDraft = null;
      this.scheduleMonth = networkTime().slice(0, 7);
      this.scheduleLoading = false;
      this.scheduleSaving = false;
      this.scheduleError = '';
    },
    async loadSchedule() {
      if (this.scheduleLoading || this.scheduleSaving) return;
      const session = this.scheduleSession;
      this.scheduleLoading = true;
      this.scheduleError = '';
      try {
        const schedule = await request('schedule');
        if (session === this.scheduleSession) this.schedule = schedule;
      } catch (error) {
        if (session === this.scheduleSession) this.showScheduleError(error);
      } finally {
        if (session === this.scheduleSession) this.scheduleLoading = false;
      }
    },
    async editAssignment(shift) {
      this.scheduleDraft = shift ? { ...shift } : emptyAssignment();
      this.scheduleError = '';
      await nextTick();
      if (this.$refs.scheduleOperator) this.$refs.scheduleOperator.focus();
    },
    async changeAssignment(method, shift) {
      if (this.scheduleSaving || this.scheduleLoading || this.role !== 'admin') return;
      if (method === 'DELETE' && !window.confirm('Удалить дежурство ' + shift.operator + '?')) return;
      const session = this.scheduleSession;
      this.scheduleSaving = true;
      this.scheduleError = '';
      this.clearSuccess();
      try {
        await request(shift.id ? 'schedule/' + shift.id : 'schedule', method, shift);
        if (session !== this.scheduleSession) return;
        if (method !== 'DELETE') this.scheduleMonth = shift.start.slice(0, 7);
        this.scheduleDraft = null;
        this.showSuccess(method === 'DELETE' ? 'Дежурство удалено' : 'Дежурство сохранено', 'schedule');
      } catch (error) {
        if (session === this.scheduleSession) this.showScheduleError(error);
        return;
      } finally {
        if (session === this.scheduleSession) this.scheduleSaving = false;
      }
      await this.loadSchedule();
    },
    showScheduleError(error) {
      if (error.status === 401) this.showError(error);
      else this.scheduleError = error.message;
    },
    resetSolutions() {
      this.solutionSession++;
      this.solutions = [];
      this.solutionQuery = '';
      this.solutionLimit = 20;
      this.solutionDraft = emptySolution();
      this.solutionFormOpen = false;
      this.solutionLoading = false;
      this.solutionSaving = false;
      this.solutionError = '';
    },
    async loadSolutions() {
      if (this.solutionLoading || this.solutionSaving) return;
      const session = this.solutionSession;
      this.solutionLoading = true;
      this.solutionError = '';
      try {
        const solutions = await request('solutions');
        if (session === this.solutionSession) this.solutions = solutions;
      } catch (error) {
        if (session === this.solutionSession) this.showSolutionError(error);
      } finally {
        if (session === this.solutionSession) this.solutionLoading = false;
      }
    },
    addSolutionVariant() {
      if (this.solutionDraft.variants.length < 4) this.solutionDraft.variants.push(emptySolutionVariant());
    },
    async submitSolution() {
      if (this.solutionSaving || this.solutionLoading || this.role !== 'engineer') return;
      const session = this.solutionSession;
      this.solutionSaving = true;
      this.solutionError = '';
      this.clearSuccess();
      try {
        const entry = await request('solutions', 'POST', this.solutionDraft);
        if (session !== this.solutionSession) return;
        this.solutions = [entry, ...this.solutions];
        this.solutionQuery = '';
        this.solutionLimit = 20;
        this.solutionDraft = emptySolution();
        this.solutionFormOpen = false;
        this.showSuccess('Решение опубликовано', 'solutions');
      } catch (error) {
        if (session === this.solutionSession) this.showSolutionError(error);
      } finally {
        if (session === this.solutionSession) this.solutionSaving = false;
      }
    },
    showSolutionError(error) {
      if (error.status === 401) this.showError(error);
      else this.solutionError = error.message;
    },
    resetStations() {
      this.stationRequest++;
      this.stations = [];
      this.stationQuery = '';
      this.stationCenter = '';
      this.stationLimit = 20;
      this.stationLoading = false;
      this.stationError = '';
    },
    async loadStations() {
      const pending = ++this.stationRequest;
      this.stationLoading = true;
      this.stationError = '';
      try {
        const stations = await request('reference/stations');
        if (pending !== this.stationRequest) return;
        this.stations = stations;
        if (!this.stationCenters.includes(this.stationCenter)) this.stationCenter = '';
        this.stationLimit = 20;
      } catch (error) {
        if (pending !== this.stationRequest) return;
        if (error.status === 401) this.showError(error);
        else this.stationError = 'Не удалось загрузить справочник.';
      } finally {
        if (pending === this.stationRequest) this.stationLoading = false;
      }
    },
    async addEngineer() {
      this.addingEngineer = true;
      this.engineerError = '';
      this.clearSuccess();
      try {
        const person = await request('engineers', 'POST', this.newEngineer);
        this.engineers = [...this.engineers, person];
        this.newEngineer = { center: '' };
        this.showSuccess('Учётная запись инженера создана', 'engineers');
      } catch (error) {
        if (error.status === 401) this.showError(error);
        else this.engineerError = error.message;
      } finally { this.addingEngineer = false; }
    },
    async loadReport(period = this.period) {
      if (this.loading || !period.from || !period.to || period.from > period.to) return;
      this.reportError = '';
      this.exportError = '';
      this.loading = true;
      const requested = { ...period };
      try {
        const { events } = await request('report?' + periodQuery(requested));
        this.report = events;
        this.shownPeriod = requested;
      } catch (error) {
        if (error.status === 401) this.showError(error);
        else this.reportError = error.message;
      }
      finally { this.loading = false; }
    },
    setReportPeriod(days) {
      this.period = { from: reportDateOffset(this.latest, 1 - days), to: this.latest };
      this.loadReport();
    },
    async downloadReport() {
      if (!this.canExport) return;
      this.exportError = '';
      this.exporting = true;
      const period = { ...this.shownPeriod };
      const format = this.exportFormat;
      try {
        const response = await fetch('/api/report.' + format + '?' + periodQuery(period), { cache: 'no-store' });
        if (!response.ok) {
          const error = new Error('Не удалось выгрузить отчёт. Повторите попытку.');
          error.status = response.status;
          throw error;
        }
        const url = URL.createObjectURL(await response.blob());
        const link = document.createElement('a');
        link.href = url;
        link.download = 'Аварии_' + period.from + (period.from === period.to ? '' : '_' + period.to) + '.' + format;
        document.body.appendChild(link);
        link.click();
        link.remove();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      } catch (error) {
        if (error.status === 401) {
          error.message = 'Сессия завершилась. Войдите снова с вашей учётной записью.';
          this.showError(error);
        } else this.exportError = 'Не удалось выгрузить отчёт. Повторите попытку.';
      } finally { this.exporting = false; }
    },
    async openIncident(event, mode) {
      this.clearSuccess();
      this.drawerMode = mode;
      this.formError = '';
      this.now = networkTime();
      this.draft = event ? {
        ...event,
        battery: formatDuration(event.battery),
        generator: formatDuration(event.generator),
        outage: formatDuration(event.outage),
        ongoing: !event.end,
      } : {
        start: this.now.slice(0,16), end: '', ongoing: true, direction: '', center: '',
        category: '', network: '', type: '', stations: '', engineerId: 0,
      };
      this.$refs.drawer.showModal();
      this.drawerVisible = true;
      document.body.classList.add('drawer-open');
      await nextTick();
      this.$refs.drawerBody.scrollTop = 0;
      this.$refs.closeButton.focus();
    },
    async saveIncident() {
      if (this.saving || this.drawerMode === 'view') return;
      const page = this.page;
      this.saving = true;
      this.formError = '';
      try {
        const id = this.draft.id;
        const payload = { ...this.draft };
        if (this.role !== 'admin') delete payload.engineerId;
        await request(id ? 'incidents/' + id : 'incidents', id ? 'PUT' : 'POST', payload);
      } catch (error) {
        this.formError = error.message;
        this.saving = false;
        if (error.status === 401) this.showError(error);
        return;
      }
      this.saving = false;
      this.showSuccess(this.draft.id ? 'Изменения сохранены' : 'Авария добавлена', page);
      this.closeIncident();
      try {
        await this.loadJournal();
        if (this.shownPeriod) await this.loadReport(this.shownPeriod);
      } catch (error) { this.showError(error); }
    },
    closeIncident() {
      if (!this.saving) this.drawerVisible = false;
    },
    finishClose() { this.$refs.drawer.close(); },
    resetDrawer() {
      this.drawerVisible = false;
      document.body.classList.remove('drawer-open');
    },
  },
}).mount('#app');
