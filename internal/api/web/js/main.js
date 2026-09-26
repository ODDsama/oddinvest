// Точка входу: монтує компонент і ставить йому транспорт.
//
// Окремим файлом, а не вбудованим <script> в index.html: тоді CSP — це
// просто script-src 'self', без хеша вбудованого модуля, який треба було
// рахувати з вшитого файла на старті й звіряти тестом.
import { httpTransport } from "./transport.js";
import { current as currentPortfolio, boot as bootPortfolio } from "./portfolio.js";
import "./app.js";

// ?p=<slug> у адресі — до першого запиту, інакше він піде в головний.
bootPortfolio();
const app = document.querySelector("odd-invest-app");
app.transport = httpTransport("/api/", currentPortfolio);
