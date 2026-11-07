# UI design system

RDP Link uses an **Ant Design 5 light theme** with Tailwind-style spacing
and surface conventions across four surfaces:

1. the Python/Tk desktop launcher,
2. the Python/Tk guest viewer,
3. the browser viewer and control panel (embedded HTML),
4. the native Android app.

The goal is that a window on macOS, Linux, Windows, a browser tab or a phone
looks like the same product.

---

## 1. Tokens

| Token | Value | Used for |
| --- | --- | --- |
| `primary` | `#1677FF` | primary buttons, links, brand mark, focus |
| `primary-hover` | `#4096FF` | hover border/text |
| `primary-active` | `#0958D9` | pressed state |
| `primary-bg` | `#E6F4FF` | selected rows, soft highlights |
| `bg-layout` | `#F5F5F5` | app background |
| `bg-container` | `#FFFFFF` | header, cards, inputs, footer |
| `text` | `#1F1F1F` (rgba 0,0,0,.88) | body text |
| `text-secondary` | `#595959` (rgba 0,0,0,.65) | labels, hints |
| `text-tertiary` | `#8C8C8C` (rgba 0,0,0,.45) | placeholders, disabled text |
| `border` | `#D9D9D9` | inputs, default buttons |
| `border-secondary` | `#F0F0F0` | card outlines, dividers |
| `success` | `#52C41A`, text `#389E0D`, bg `#F6FFED`, border `#B7EB8F` | live/sharing tags |
| `warning` | `#FAAD14`, text `#D48806`, bg `#FFF7E6`/`#FFFBE6` | connecting/reconnecting |
| `error` | `#FF4D4F`, text `#CF1322`, bg `#FFF2F0`, border `#FFCCC7` | disconnected, danger |
| radius | `6px` controls, `8px` cards | buttons, inputs, cards |
| shadow | `0 1px 2px rgba(0,0,0,.03), 0 1px 6px -1px rgba(0,0,0,.02), 0 2px 4px rgba(0,0,0,.02)` | cards |
| font | `-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "Noto Sans", sans-serif` | all text |

Spacing follows a 4 px scale: 4 / 8 / 12 / 16 / 20 / 24. Control height is
32 px (28 px compact, 40 px large); cards use 20–24 px padding.

---

## 1.1 Brand mark

The supplied lockup (`../app/assets/logo-full.png`, a monitor with outgoing
chevrons above the "RDP" wordmark) is cropped to the monitor+chevrons square
for small sizes:

| Asset | Size | Used by |
| --- | --- | --- |
| `app/assets/logo-mark.png` | 1024 | master |
| `app/assets/logo-mark-128.png` | 128 | launcher/viewer (Tk, subsampled to 32) |
| `app/assets/icon.iconset` → `icon.icns` | 16–1024 | macOS bundle icon |
| `app/assets/icon.ico` | 16–256 | Windows executable/installer |
| `mipmap-*/ic_launcher*.png` | 48–192 | Android launcher |
| `core/host/logo.png` | 128 | `/logo.png` for the browser brand |

The header wordmark is two-tone: "RDP" in `text`, "Link" in `primary`. On
narrow browser viewports the wordmark hides and the mark remains.

---

## 2. Layout

Every surface follows the Ant Design `Layout` skeleton:

```
┌──────────────────────────────────────────────┐
│ Header (white, 56–64 px, 1 px #F0F0F0 rule)  │
│  [mark] Title        status tag   actions    │
├──────────────────────────────────────────────┤
│ Content (#F5F5F5, 16–24 px padding)          │
│  ┌ Card ──────────────────────────────────┐  │
│  │ section title                          │  │
│  │ label / value / input / actions        │  │
│  └────────────────────────────────────────┘  │
├──────────────────────────────────────────────┤
│ Footer (white, hairline top border)          │
└──────────────────────────────────────────────┘
```

The header is a white bar with a hairline divider, not a coloured hero. Cards
sit on the grey layout background and use a 1 px `border-secondary` outline
(plus a soft shadow on web/Android).

---

## 3. Components

### Buttons

| Variant | Background | Border | Text |
| --- | --- | --- | --- |
| default | `#FFFFFF` | `#D9D9D9` | `#1F1F1F`; hover text/border `#4096FF` |
| primary | `#1677FF` | `#1677FF` | `#FFFFFF`; hover `#4096FF`, pressed `#0958D9` |
| danger | `#FFFFFF` | `#FFCCC7` | `#CF1322`; pressed fills `#FF4D4F` |
| disabled | `#F5F5F5` | `#D9D9D9` | `#BFBFBF` |

### Tag / status pill

Ant Design tag geometry: 4 px radius, 1 px border, 12–13 px text, 8 px
horizontal padding. Semantic colour maps to state:

- **Sharing / live** → success palette
- **Connecting / reconnecting** → warning palette
- **Disconnected / error** → error palette

### Devices drawer

A header **Devices** button opens a 360 px Ant Design drawer with sections for
speaker, microphone, camera, serial and allow-listed ports. Section visibility
comes from `/capabilities`; unavailable features are hidden rather than shown
disabled. The drawer uses the same tokens as the rest of the viewer.

### Scale selector

Both viewers expose **Fit**, **Fill**, **1:1**, 50 %, 75 %, 125 % and 150 %.
Fit scales up as well as down; Fill covers the stage and crops; fixed values
pin the frame to `host resolution × scale`. The control is a 32 px Ant Design select
(white fill, `#D9D9D9` border, 6 px radius). Auto fit recalculates on resize
and orientation change; any fixed value pins the frame to
`host resolution × scale` and makes the stage scroll (browser `overflow:auto`,
Tk canvas scrollbars). Coordinate mapping subtracts the scroll offset, so
input stays accurate at every scale.

### Inputs

32–40 px height, 6 px radius, `#D9D9D9` border, white fill. Focus switches the
border to `#1677FF` and adds a 2 px `rgba(5,145,255,.1)` ring.

### Table (desktop launcher)

Ant Design table look: 40 px rows, `#FAFAFA` header with secondary text,
1 px `#F0F0F0` separators, zebra `#FAFAFA` rows, selection `#E6F4FF`, no
vertical grid lines. Connected profiles use a soft green fill and green text.

### Cards

8 px radius, white fill, `#F0F0F0` outline, 20–24 px padding, 13–16 px section
title in semibold. No heavy elevation on desktop; web/Android add the token
shadow.

---

## 4. Per-surface mapping

### 4.1 Browser (`core/host/viewer.html`, `core/host/ui.html`)

Tokens live in CSS custom properties (`:root`). Buttons, inputs, cards and tags
are plain elements styled with those variables — no framework is shipped.
The header collapses the brand text (keeping the mark) below 760 px and wraps
to two rows instead of truncating the status tag.

### 4.2 Tk (`app/rdp_manager/ui/*.py`, `app/office_viewer.py`)

ttk does not support border radii, so the mapping keeps colour, spacing,
typography and 1 px borders:

- `clam` theme; `TFrame`/`TLabel` on `#F5F5F5`.
- `TButton` = white, `#D9D9D9` border, 1 px `solid` relief; `Accent.TButton`
  is the blue primary; `Danger.TButton` the error outline.
- `Treeview` = Ant Design table (40 px rows, `#FAFAFA` header, zebra tags,
  `#E6F4FF` selection).
- Header and footer are real white `tk.Frame` bars with hairline dividers;
  the content area keeps the grey background.
- Cards are white `tk.Frame`s with a 1 px `#F0F0F0` outline.
- The viewer toolbar uses `Label`-based chips with hover colour (native
  `tk.Button` ignores colours on macOS Aqua), a blue mark, a tag-coloured
  status pill, and a light stage.

### 4.3 Android (`res/values`, `res/drawable`, `res/layout`)

- `colors.xml` holds the palette; `styles.xml` defines `AppTheme` and
  `OfficeButton`, `OfficeButtonPrimary`, `OfficeButtonDanger`, `OfficeInput`,
  `OfficeCard`, `OfficeLabel`, `OfficeTag`.
- Rounded corners come from shape/selector drawables (6 dp controls, 8 dp
  cards, 4 dp tags) with pressed states.
- The screen is a white header, a hairline divider, grey content with two
  cards, and a two-button action row.
- Status is rendered as a tag and changes background/text with state
  (`bg_tag_success`, `bg_tag_error`, `bg_tag_neutral`).

### 4.4 Windows

The Windows launcher is the same Python/Tk application frozen with PyInstaller,
so it inherits the desktop mapping exactly; only font metrics differ under
Windows/Wine.

---

## 4.5 New workflow components (2026)

The following components were added to support the tabbed/expandable workflow
across browser, desktop and Android. They reuse the tokens in §1.

- **Tabs (top)**: 40 px tab bar, blue active indicator, lazy content. Used by the
  browser control panel (Share/Connect/Devices/Files/Settings) and the desktop
  `ttk.Notebook` (Share/Connect/Devices/Files/Settings).
- **Bottom navigation (Android)**: 4 items (Home/Devices/Files/Settings), icon +
  label, active colour `primary`.
- **Accordion / expandable card**: chevron toggle, animated height, remembers open
  state. Used for advanced settings (port, rendezvous, permissions).
- **Modal popup**: centered card, scrim, Esc/back to close, focus trap. Used for
  permission setup, destructive actions, text-to-type.
- **Bottom sheet (Android)**: slides up for device actions and "more keys".
- **Slider**: 4 px track, 12 px thumb, snap points, value tooltip. Used for
  viewer zoom (50–150 %) alongside a segmented control (Fit/Fill/1:1).
- **Segmented control**: compact alternative to slider snaps.
- **Chip**: tappable, removable. Used for recent devices and tags.
- **Dark mode**: toggle in Settings on every surface; light default. Browser
  applies via `data-theme`; desktop persists preference; Android persists
  preference.

Mockups: `docs/mockups/`.

## 5. Accessibility and behaviour notes

- Colour is never the only signal: status tags also change text.
- Focus states are visible on web and Android; Tk relies on the platform focus
  ring.
- Touch targets are at least 36–40 px high on Android and 32 px on desktop.
- The browser viewer keeps the remote image at native aspect ratio in fit mode
  and switches to 1:1 without cropping.
- All surfaces respect a narrow viewport: the browser header wraps, the Tk
  layout keeps the footer pinned and lets the table shrink, and Android uses a
  scrollable content column.
- **Portrait and landscape** are both supported. The Android activities declare
  `configChanges` for orientation/screen size (the WebView keeps its session),
  and the browser viewer reflows on `orientationchange`. Desktop windows simply
  re-render on resize.
- **Display scaling** is a first-class control: Fit by default, with Fill,
  1:1 and 50–150 % steps and scrolling when the scaled host exceeds the stage.
- A **host display picker** appears when the host has more than one monitor;
  the Tk viewer also offers **Match window** to size the window to the host
  aspect ratio.
- **Android layout:** a white header, two cards, a collapsed Advanced panel and
  a sticky bottom action bar (Stop / Start sharing / Connect). Recent devices
  are a tappable list; long-press removes an entry. Landscape uses a
  two-column layout (`layout-land`) with the same components.
