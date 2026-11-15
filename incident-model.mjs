export const catalogs = {
  direction: ['ХМЯН', 'СВН', 'ЮЗН', 'Вся сеть'],
  center: ['НБР','АСБ','ИРБ','КРТ','НТГ','НТР','НУРЛ','НВРТ','НУРГ','ОКТ','СЛХ','СОВ','СРГ','ХМН','ЕКБ','КМУ','КРГ','НСР'],
  category: ['1', '2', '3'],
  network: ['GSM', 'LTE', 'LTE/GSM'],
  type: ['РРС','ССС','ЭЛ','RBS','МСПД','ТМ','ОПС','ПР','АВР','Аренда'],
};

export function parseDuration(value) {
  if (!value) return null;
  const parts = /^(\d{1,10})([:.])([0-5]\d)\2([0-5]\d)$/.exec(value.trim());
  return parts ? Number(parts[1]) * 3600 + Number(parts[3]) * 60 + Number(parts[4]) : NaN;
}

export function formatDuration(seconds) {
  if (!Number.isFinite(seconds) || seconds < 0) return '';
  return [Math.floor(seconds / 3600), Math.floor(seconds / 60) % 60, seconds % 60]
    .map(value => String(value).padStart(2, '0')).join(':');
}

export function networkTime(now = new Date()) {
  // Network timestamps in the workbook and database are Yekaterinburg wall time.
  return new Date(now.getTime() + 5 * 3600 * 1000).toISOString().slice(0, 19);
}

export function calculate({ start, end, stations, outage }, now = networkTime()) {
  const begin = Date.parse(start + 'Z');
  const finish = Date.parse((end || now) + 'Z');
  const duration = Number.isFinite(begin) && Number.isFinite(finish) && finish >= begin
    ? Math.floor((finish - begin) / 1000) : null;
  const count = /^[сc]$/i.test(stations) ? 1 : /^[0-9]+$/.test(stations) ? Number(stations) : null;
  const total = count !== null && Number.isFinite(outage) ? count * outage : null;
  return { duration, total };
}
