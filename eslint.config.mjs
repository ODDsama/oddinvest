// Перевірка фронтенду на невизначені імена. Тільки для CI.
//
// Чому вона тут узагалі зʼявилась. Джоба `ui` перевіряє синтаксис кожного
// модуля і те, що КОЖЕН імпорт резолвиться, — і цього досить, доки код не
// розкладають по файлах. Але після розбиття portfolio.js на модулі чотири
// файли лишились із `fmtCur(...)` у тілі й без цього імені в шапці
// імпорту: модуль чудово завантажувався, граф імпортів був цілий, а розділ
// «Портфель» падав у браузері з «fmtCur is not defined». Саме такий промах
// найлегше зробити з псевдонімом (`cur2 as fmtCur`) — і саме його жодна з
// наявних перевірок не бачила.
//
// Конфіг лежить у корені репозиторію, а не поруч із модулями, бо
// internal/api/web цілком вшивається в бінарник директивою `//go:embed web`
// — файл звідти поїхав би користувачам у браузер.
//
// Глобальні перелічені руками навмисно: пакет `globals` був би залежністю,
// а список тут короткий і чесно показує, на що застосунок спирається.
export default [
  {
    files: ["internal/api/web/js/**/*.js"],
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: "module",
      globals: {
        document: "readonly",
        Document: "readonly",
        window: "readonly",
        console: "readonly",
        localStorage: "readonly",
        fetch: "readonly",
        // Заголовок портфеля (transport.js) і ?p= у адресі (portfolio.js).
        Headers: "readonly",
        URLSearchParams: "readonly",
        setTimeout: "readonly",
        clearTimeout: "readonly",
        confirm: "readonly",
        alert: "readonly",
        matchMedia: "readonly",
        getComputedStyle: "readonly",
        requestAnimationFrame: "readonly",
        HTMLElement: "readonly",
        customElements: "readonly",
        CSSStyleSheet: "readonly",
        MouseEvent: "readonly",
        Event: "readonly",
        Node: "readonly",
        URL: "readonly",
        Blob: "readonly",
        FormData: "readonly",
        Intl: "readonly",
        AbortController: "readonly",
      },
    },
    linterOptions: { reportUnusedDisableDirectives: true },
    rules: {
      "no-undef": "error",
      // Невживаний імпорт — той самий клас помилки, лише з іншого боку:
      // після переїзду функції шапка лишається з іменем, якого в файлі вже
      // немає. Аргументи не чіпаємо: у колбеках вони часто для форми.
      "no-unused-vars": ["error", { args: "none", caughtErrors: "none" }],
    },
  },
];
