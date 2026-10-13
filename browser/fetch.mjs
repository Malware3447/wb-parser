// Открывает поиск Wildberries в CloakBrowser и забирает ответ поискового API.
// Запуск: node fetch.mjs <input.json> <output.ndjson>
//   input.json:    {"queries": ["куртка", ...], "headed": false}
//   output.ndjson: по строке на запрос — {"query", "ok", "error", "url", "data"}
import fs from 'node:fs';
import { launch } from 'cloakbrowser';

const [inputPath, outputPath] = process.argv.slice(2);
if (!inputPath || !outputPath) {
  console.error('Запуск: node fetch.mjs <input.json> <output.ndjson>');
  process.exit(2);
}
const { queries, headed } = JSON.parse(fs.readFileSync(inputPath, 'utf8'));

const ATTEMPTS = 3;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Нужен именно ответ с товарами: тот же адрес отдаёт ещё и фильтры.
const isSearchResponse = (r) => {
  const u = r.url();
  return u.includes('/search?') && u.includes('query=') && u.includes('resultset=catalog') && r.status() === 200;
};

async function fetchQuery(page, query) {
  const wait = page.waitForResponse(isSearchResponse, { timeout: 90000 });
  wait.catch(() => {});
  await page.goto('https://www.wildberries.ru/catalog/0/search.aspx?search=' + encodeURIComponent(query), {
    waitUntil: 'domcontentloaded',
    timeout: 60000,
  });
  const resp = await wait;
  const data = await resp.json();
  if (!Array.isArray(data.products)) throw new Error('в ответе нет списка товаров');
  return { url: resp.url(), data };
}

const out = fs.openSync(outputPath, 'w');
const browser = await launch({ headless: !headed, locale: 'ru-RU', timezone: 'Europe/Moscow' });
let failed = 0;
try {
  const page = await browser.newPage();
  for (const [i, query] of queries.entries()) {
    let result = { query, ok: false, error: '' };
    for (let attempt = 1; attempt <= ATTEMPTS; attempt++) {
      try {
        result = { query, ok: true, ...(await fetchQuery(page, query)) };
        break;
      } catch (e) {
        result.error = String(e.message).split('\n')[0];
        console.error(`  попытка ${attempt}/${ATTEMPTS} не удалась: ${result.error}`);
        await sleep(5000 * attempt);
      }
    }
    if (!result.ok) failed++;
    fs.writeSync(out, JSON.stringify(result) + '\n');
    console.error(`[${i + 1}/${queries.length}] ${query}: ${result.ok ? result.data.products.length + ' товаров' : 'ОШИБКА'}`);
    if (i < queries.length - 1) await sleep(2000 + Math.random() * 2000);
  }
} finally {
  fs.closeSync(out);
  await browser.close();
}
process.exit(failed ? 1 : 0);
