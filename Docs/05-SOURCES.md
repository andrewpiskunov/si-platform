# Источники и достоверность

Проверка: **20 октября 2026 года**. Использованы страницы продуктов, исходный код авторов и официальная документация. Содержимое сторонних пакетов не копировалось в этот репозиторий.

## Продукты и совместимость

| Источник | Для чего использован |
|---|---|
| [SIGame](https://sigame.vladimirkhil.com/) | Проверка доступности веб-точки входа; HTML — оболочка JS-приложения |
| [SIOnline](https://github.com/VladimirKhil/SIOnline) | Веб-клиент, стек, структура и лицензия |
| [Онлайн-редактор](https://github.com/VladimirKhil/SIOnline/blob/ac2e66190056beb141b450b9cb044e0e1e99aa6d/src/components/views/SIQuester/SIQuester.tsx) | Создание/открытие пакета в исходном коде |
| [Настройки](https://github.com/VladimirKhil/SIOnline/blob/ac2e66190056beb141b450b9cb044e0e1e99aa6d/src/model/AppSettings.ts), [кнопка](https://github.com/VladimirKhil/SIOnline/blob/ac2e66190056beb141b450b9cb044e0e1e99aa6d/src/model/ButtonPressMode.ts), [таймеры](https://github.com/VladimirKhil/SIOnline/blob/ac2e66190056beb141b450b9cb044e0e1e99aa6d/src/model/TimeSettings.ts) | Сопоставление игровых возможностей |
| [SI Browser](https://www.sibrowser.ru/), [карточка](https://www.sibrowser.ru/packages/25877), [robots.txt](https://www.sibrowser.ru/robots.txt) | Публичный каталог, происхождение, канал публикации и правило обхода |
| [SIQuester 6.6.0](https://github.com/VladimirKhil/SI/releases/tag/siquester-v6.6.0), [6.9.0](https://github.com/VladimirKhil/SI/releases/tag/siquester-v6.9.0), [релизы](https://github.com/VladimirKhil/SI/releases) | ИИ-функции и развитие формата |
| [SIQ v4](https://github.com/VladimirKhil/SI/wiki/SIQ-file-format-%28version-4%29), [SIQ v5](https://github.com/VladimirKhil/SI/wiki/SIQ-file-format-version-5), [миграция](https://github.com/VladimirKhil/SI/wiki/Migrating-SIQ-file-from-version-4-to-version-5) | Структура и различия формата |
| [SIPackages](https://github.com/VladimirKhil/SI/blob/7f8cda975fdd71aef69fa4eef0572d41417d8900/src/Common/SIPackages/README.md), [XSD](https://github.com/VladimirKhil/SI/blob/7f8cda975fdd71aef69fa4eef0572d41417d8900/assets/siq_5.xsd), [типы вопросов](https://github.com/VladimirKhil/SI/blob/7f8cda975fdd71aef69fa4eef0572d41417d8900/src/Common/SIPackages/Core/QuestionTypes.cs) | Первичная схема и реализация модели пакета |
| [SIPacker](https://git.hloth.dev/hloth/SIPacker) | Дополнительный пример веб-редактора; авторское описание, не независимый тест |
| [MIT: SI](https://github.com/VladimirKhil/SI/blob/7f8cda975fdd71aef69fa4eef0572d41417d8900/LICENSE), [GPL-3.0: SIOnline](https://github.com/VladimirKhil/SIOnline/blob/ac2e66190056beb141b450b9cb044e0e1e99aa6d/LICENSE) | Различие лицензий компонентов |

Исходники прочитаны на коммитах SIOnline `ac2e66190056beb141b450b9cb044e0e1e99aa6d` и SI `7f8cda975fdd71aef69fa4eef0572d41417d8900`. Не утверждается, что эти коммиты совпадают с текущими production-развёртываниями.

## Технические и правовые основания

| Источник | Для чего использован |
|---|---|
| [WebSocket](https://developer.mozilla.org/en-US/docs/Web/API/WebSockets_API), [WebTransport](https://developer.mozilla.org/en-US/docs/Web/API/WebTransport) | Транспорт, браузерная поддержка и управление очередями |
| [Autoplay](https://developer.mozilla.org/en-US/docs/Web/Media/Guides/Autoplay), [WCAG 2.2](https://www.w3.org/TR/WCAG22/) | Поведение медиа и ориентир доступности |
| [Svelte](https://svelte.dev/docs/svelte/overview), [SvelteKit](https://svelte.dev/docs/kit/page-options) | Компиляция и режимы рендеринга |
| [Go FAQ](https://go.dev/doc/faq#goroutines), [Go GC](https://go.dev/doc/gc-guide) | Goroutine, потоки и управление памятью выбранного серверного языка |
| [MCP architecture](https://modelcontextprotocol.io/specification/2025-11-25/architecture), [transports](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports), [tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools) | Роли, Streamable HTTP и инструменты; изученная версия спецификации 2025-11-25 |
| [WIPO: Copyright FAQ](https://www.wipo.int/en/web/copyright/faq-copyright) | Общее различие авторства, публикации в интернете и разрешения; не заключение о конкретных паках |

Источники по конкретным Go-библиотекам, объектным хранилищам и их ограничениям приведены рядом с выводами в [анализе стека](06-TECH-STACK.md). Официальные документы SvelteKit, React Router, Next.js и Vue/Nuxt — в [сравнении фронтенда](07-FRONTEND-CHOICE.md).

Требование готовых и загружаемых тем поступило от пользователя. Формат архива, охват экранов и лимиты — наши проектные предложения в [контракте оформления](08-THEMES.md), а не заявления о возможностях конкурентов. Технические основания: [CSS custom properties](https://developer.mozilla.org/en-US/docs/Web/CSS/Guides/Cascading_variables/Using_custom_properties) и [prefers-reduced-motion](https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/At-rules/@media/prefers-reduced-motion).

## Как читать выводы

**Факт** — подтверждён страницей, кодом или спецификацией. **Рекомендация** — наш предлагаемый выбор. **Гипотеза** — требует интервью, прототипа или измерений.

Не измерены скорость конкурентов, потребление памяти, стоимость инфраструктуры, аудитория и полное число пакетов. Не подтверждены массовый API/экспорт SI Browser и права на перенос. Юрисдикция проекта ещё не выбрана. Цели нагрузки, размеры стенда, продуктовые показатели и лимиты файлов — проектные предложения.

Изучение сайтов не включало авторизованный игровой сеанс или полное визуальное UX-тестирование. Прототипы интерфейса и тесты на реальных устройствах включены в следующую аналитику.
