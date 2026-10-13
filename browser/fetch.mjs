// Открывает Wildberries в CloakBrowser и забирает ответы его API: выдачу по запросам и карточки товаров.
// Запуск: node fetch.mjs <input.json> <output.ndjson>
//   input.json:    {"queries": ["куртка", ...], "cards": [1434234801, ...], "dest": -1257786, "headed": false}
//   output.ndjson: по строке на запрос и на карточку —
//                  {"kind": "query"|"card", "query"|"id", "ok", "error", "url", "data"}
import fs from 'node:fs';
import { launch } from 'cloakbrowser';

const [inputPath, outputPath] = process.argv.slice(2);
if (!inputPath || !outputPath) {
  console.error('Запуск: node fetch.mjs <input.json> <output.ndjson>');
  process.exit(2);
}
const { queries = [], cards = [], dest, headed } = JSON.parse(fs.readFileSync(inputPath, 'utf8'));

const ATTEMPTS = 3;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Страница открывается, а нужный ответ API ловится по адресу
async function capture(page, pageUrl, isWanted) {
  const wait = page.waitForResponse((r) => r.status() === 200 && isWanted(r.url()), { timeout: 90000 });
  wait.catch(() => {});
  await page.goto(pageUrl, { waitUntil: 'domcontentloaded', timeout: 60000 });
  const resp = await wait;
  const data = await resp.json();
  if (!Array.isArray(data.products)) throw new Error('в ответе нет списка товаров');
  return { url: resp.url(), data };
}

// Нужен именно ответ с товарами: тот же адрес отдаёт ещё и фильтры
const fetchQuery = (page, query) =>
  capture(page, 'https://www.wildberries.ru/catalog/0/search.aspx?search=' + encodeURIComponent(query),
    (u) => u.includes('/search?') && u.includes('query=') && u.includes('resultset=catalog'));

// Следом страница запрашивает соседние артикулы той же карточки — нужен ответ только про наш
const fetchCard = (page, id) =>
  capture(page, `https://www.wildberries.ru/catalog/${id}/detail.aspx`,
    (u) => u.includes('/cards/') && u.includes('/detail?') && u.endsWith('nm=' + id));

const tasks = [
  ...queries.map((query) => ({ kind: 'query', query, label: query, run: (page) => fetchQuery(page, query) })),
  ...cards.map((id) => ({ kind: 'card', id, label: 'карточка ' + id, run: (page) => fetchCard(page, id) })),
];

const out = fs.openSync(outputPath, 'w');
const browser = await launch({ headless: !headed, locale: 'ru-RU', timezone: 'Europe/Moscow' });
let failed = 0;
try {
  const page = await browser.newPage();
  // Регион сайт берёт из своих настроек; нужный подставляется прямо в запросы к API
  if (dest) {
    await page.route(/__internal\/(u-search|u-card)\//, (route) =>
      route.continue({ url: route.request().url().replace(/dest=-?\d+/, 'dest=' + dest) }));
  }
  for (const [i, task] of tasks.entries()) {
    const { run, label, ...head } = task;
    let result = { ...head, ok: false, error: '' };
    for (let attempt = 1; attempt <= ATTEMPTS; attempt++) {
      try {
        result = { ...head, ok: true, ...(await run(page)) };
        break;
      } catch (e) {
        result.error = String(e.message).split('\n')[0];
        console.error(`  попытка ${attempt}/${ATTEMPTS} не удалась: ${result.error}`);
        await sleep(5000 * attempt);
      }
    }
    if (!result.ok) failed++;
    fs.writeSync(out, JSON.stringify(result) + '\n');
    console.error(`[${i + 1}/${tasks.length}] ${label}: ${result.ok ? result.data.products.length + ' товаров' : 'ОШИБКА'}`);
    if (i < tasks.length - 1) await sleep(2000 + Math.random() * 2000);
  }
} finally {
  fs.closeSync(out);
  await browser.close();
}
process.exit(failed ? 1 : 0);
