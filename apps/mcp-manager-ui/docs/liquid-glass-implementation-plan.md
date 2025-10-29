**Liquid Glass UI Implementation Plan (Wails + React + Tailwind v4 + shadcn)**

Goal: Restyle the current desktop UI to Apple‑inspired Liquid Glass (glassy, frosted, luminous text, soft specular edges, hairline strokes) while preserving accessibility, performance, and theming. The plan is specific to the current code under `frontend/` and names exactly where to add utilities and how to apply them to existing components.

---

**Inventory (Current State)**
- Global styles: `frontend/src/index.css` with CSS variables, Tailwind v4 `@theme inline`, fixed scenic background image (`/public/bg2.png`), base radius `--radius: 0.625rem`.
- Components (shadcn‑style):
  - `components/ui/button.tsx` (cva variants: default, destructive, outline, secondary, ghost, link)
  - `components/ui/badge.tsx` (variants: default, secondary, destructive, outline)
  - `components/ui/card.tsx` (no variant system; structural slots)
  - `components/ui/table.tsx`, `separator.tsx`
- Screens: `src/App.tsx` renders dashboard header, stat cards, server cards, refresh button.

---

**Design Targets (Liquid Glass Traits)**
- Translucent frosted panels with strong backdrop blur and saturation.
- Soft outer drop shadow + subtle inner edge highlight; 1px hairline border with top‑left highlight and bottom‑right shadow (specular rim).
- Tinted glass fills (neutral base; status/brand tints for emphasis).
- Luminous “liquid” headings with vertical gradient text and soft glow.
- Micro‑noise over glass to avoid banding; animated specular highlight on hover for pressables.
- Dark mode parity and reduced‑transparency fallback.

---

**Phase 1 — Tokens, Layers, and Utilities**

Files: `frontend/src/index.css` (extend), `frontend/public/` (optional noise asset)

1) Add Liquid Glass CSS variables
- Place under `:root` and `.dark` in `index.css`:
  - `--lg-blur: 28px;` (dark: `32px`)
  - `--lg-saturate: 1.75;`
  - `--lg-noise-alpha: 0.035;` (dark: `0.05`)
  - `--lg-surface: color-mix(in oklab, var(--background) 45%, transparent);`
  - `--lg-border-hi: color-mix(in oklab, white 65%, transparent);`
  - `--lg-border-lo: color-mix(in oklab, black 35%, transparent);`
  - `--lg-shadow: 0 20px 40px -20px oklch(0 0 0 / 35%);`
  - `--lg-inner: inset 0 1px oklch(1 0 0 / 35%), inset 0 -1px oklch(0 0 0 / 10%);`
  - Text vars: `--lg-text-top`, `--lg-text-bottom`, `--lg-text-spec` tuned per theme.

2) Add utility classes (component‑level building blocks)
- Append to `index.css` under `@layer utilities`:
```
.glass {
  background:
    linear-gradient(180deg, oklch(1 0 0 / 55%) 0%, oklch(1 0 0 / 20%) 100%),
    var(--lg-surface);
  -webkit-backdrop-filter: saturate(var(--lg-saturate)) blur(var(--lg-blur));
  backdrop-filter: saturate(var(--lg-saturate)) blur(var(--lg-blur));
}
.glass-noise::after {
  content: ""; position: absolute; inset: 0; pointer-events: none;
  background-image: radial-gradient(oklch(0 0 0 / var(--lg-noise-alpha)) 1px, transparent 1px);
  background-size: 2px 2px; border-radius: inherit;
}
.glass-border {
  position: relative; border-radius: inherit;
  box-shadow: var(--lg-shadow), var(--lg-inner);
}
.glass-border::before { /* Hairline + specular rim */
  content: ""; position: absolute; inset: 0; border-radius: inherit;
  padding: 1px; mask: linear-gradient(#000, #000) content-box, linear-gradient(#000, #000);
  -webkit-mask: linear-gradient(#000,#000) content-box, linear-gradient(#000,#000);
  -webkit-mask-composite: xor; mask-composite: exclude;
  background: linear-gradient(180deg, var(--lg-border-hi), var(--lg-border-lo));
}
.glass-pressable { transition: transform .2s ease, box-shadow .2s ease, background .2s ease; }
.glass-pressable:hover { transform: translateY(-1px); box-shadow: 0 24px 40px -22px oklch(0 0 0 / 45%), var(--lg-inner); }
.glass-pressable:active { transform: translateY(0); }
.text-liquid { /* luminous gradient heading */
  background: linear-gradient(180deg, var(--lg-text-top), var(--lg-text-bottom));
  -webkit-background-clip: text; background-clip: text; color: transparent;
  filter: drop-shadow(0 1px 0 var(--lg-text-spec));
}
.hairline { border-color: color-mix(in oklab, var(--foreground) 10%, transparent) !important; }
```

3) Radius and elevation defaults
- Increase `--radius` from `0.625rem` to `0.875rem` (14px) or `1rem` (16px) for more liquid feel.
- Add `--radius-2xl` in `@theme inline` if needed for larger panels.

4) Reduced transparency support
- Add under `@media (prefers-reduced-transparency: reduce)` a fallback that replaces `.glass` with solid `bg-card/95` and disables `backdrop-filter`.

5) Optional: Wallpaper‑aware tint
- Keep scenic background. Add subtle overlay gradient on `body` to harmonize: `background-image: linear-gradient(0deg, oklch(0 0 0 / 10%), transparent), url(/public/bg2.png);`

---

**Phase 2 — Component Variants (shadcn cva)**

Files: `components/ui/*.tsx`

1) Card
- Introduce a `variant` prop via `cva`:
  - `appearance: 'glass' | 'solid'` (default `'glass'`).
  - Classes (glass): `relative glass glass-border glass-noise rounded-2xl border border-transparent`.
  - Solid fallback uses current `bg-card` / `border` classes.
- Update `Card`, `CardHeader`, `CardContent`, `CardFooter` to keep padding; only container gets glass classes.

2) Button
- Add `variant: 'glass' | 'tinted' | ...existing`.
  - `glass`: `relative glass glass-border glass-pressable border-transparent text-foreground/90` with extra `px` for comfort.
  - `tinted`: same as `glass` but add `background: linear-gradient(180deg, oklch(0.98 0 0 / .55), oklch(0.98 0 0 / .25)), color-mix(in oklab, var(--primary) 25%, transparent);` and `text-primary-foreground`.
  - Keep `outline` as non‑translucent alternative.

3) Badge
- Add `variant: 'glass'` → `glass glass-border px-2 py-0.5 text-xs` with toned down noise (`--lg-noise-alpha: 0.02`). For status badges, compute tint from semantic tokens (e.g., green for connected).

4) Table
- Reduce visual weight to fit glass: rows no background hover; use hairline separators and muted text.
- Apply `.hairline` to row borders, and use `backdrop-blur-0` so text stays crisp.

5) Separator
- Use `bg-white/25 dark:bg-white/10` and `.hairline` for a true hairline.

Implementation pattern (example for Button `cva`):
```ts
// frontend/src/components/ui/button.tsx
const buttonVariants = cva(
  "inline-flex items-center justify-center rounded-md text-sm font-medium transition-all ...",
  { variants: {
      variant: {
        glass: "relative glass glass-border glass-pressable border-transparent text-foreground/90",
        tinted: "relative glass glass-border glass-pressable text-primary-foreground",
        // existing variants...
      },
      size: { /* unchanged */ }
    }, defaultVariants: { variant: "glass", size: "default" }
  }
)
```

---

**Phase 3 — Screen‑Level Adoption**

Files: `src/App.tsx`

- Header
  - Apply `.text-liquid` to the `h1` and increase weight to `font-semibold` (already present). Add `tracking-tight` (already present).
  - Subtitle stays `text-muted-foreground` but increase contrast slightly for readability on glass.

- Toolbar
  - Change Refresh button to `variant="glass"` (new) and keep the icon.
  - Timestamp chip → convert to a `Badge` with `variant="glass"` or keep as plain text if you prefer minimalism.

- Stat Cards
  - Render each `Card` with `appearance="glass"` and larger radius `rounded-2xl`.
  - Optionally add subtle background tint per stat on hover using `hover:before:opacity-100` (see Phase 4 effects).

- Server Cards
  - `Card` uses `appearance="glass"`.
  - Status `Badge` uses `variant="glass"` with tint mapping:
    - connected → green tint; connecting → amber; disconnected → red (use existing OKLCH chart tokens or add semantic tints).
  - Table rows: apply `.hairline` on `TableRow` borders; labels remain `uppercase text-xs text-muted-foreground`.

---

**Phase 4 — Micro‑Effects and Tints**

Files: `index.css` additions only

**Pre-Phase 4 Adjustments (after first visual pass)**
- Strengthen glass contrast: brighten the top rim and deepen the base gradient in `.glass`/`.glass-border` so panels feel luminous instead of matte.
- Make `text-liquid` more pronounced with a darker bottom stop and slightly stronger drop shadow.
- Add reusable tint helpers (`.tint-success`, `.tint-warn`, `.tint-danger`) with translucent fills + faint glow so state badges still read as glass.
- Implement the specular hover streak using `.glass-pressable::before` and apply to cards and primary buttons.
- Rebalance table sheets: keep translucent fill but restore clearer hairline borders and spacing.
- Revisit noise strength (adjust `--lg-noise-alpha`) to avoid overly smooth, plastic surfaces.

1) Interactive specular highlight for pressables
```
.glass-pressable::before {
  content:""; position:absolute; inset:0; border-radius:inherit; pointer-events:none;
  background: radial-gradient(120% 180% at 50% -40%, oklch(1 0 0 / .35), transparent 40%),
              linear-gradient(180deg, oklch(1 0 0 / .15), transparent 40%);
  opacity:.0; transition: opacity .2s ease;
}
.glass-pressable:hover::before { opacity:.8; }
```

2) Semantic tints
```
.tint-success { background-color: color-mix(in oklab, var(--chart-4) 24%, transparent); }
.tint-warn    { background-color: color-mix(in oklab, var(--chart-5) 24%, transparent); }
.tint-danger  { background-color: color-mix(in oklab, var(--destructive) 28%, transparent); }
```

3) Liquid text token values
- Light: `--lg-text-top: oklch(0.18 0 0); --lg-text-bottom: oklch(0.08 0 0); --lg-text-spec: oklch(1 0 0 / 65%);`
- Dark:  `--lg-text-top: oklch(0.98 0 0); --lg-text-bottom: oklch(0.86 0 0); --lg-text-spec: oklch(1 0 0 / 35%);`

---

**Phase 5 — Accessibility, Fallbacks, and Performance**
- Contrast: Ensure 4.5:1 for body text on glass; if a card overlays bright wallpaper, increase `--lg-surface` mix to 60% and `--lg-border-lo` to improve edge definition.
- Reduced motion: use existing transitions; no large parallax or shimmering animations by default.
- Reduced transparency: `@media (prefers-reduced-transparency: reduce)` → replace `.glass` with solid `bg-card/95` and disable noise overlay.
- GPU cost: `backdrop-filter` is expensive on very large layers; keep panels sized to content, avoid page‑sized frosted containers.

---

**Concrete Code Changes (File‑by‑File)**

- `frontend/src/index.css`
  - Add all variables and utilities from Phases 1 & 4.
  - Raise `--radius` to `1rem` and introduce `--radius-2xl` if desired.
  - Optional: add gradient overlay to body background to soften wallpaper contrast.

- `frontend/src/components/ui/card.tsx`
  - Introduce `cva` with `appearance` variant: `'glass' | 'solid'`.
  - Default to `'glass'` for this app; allow override for content requiring high contrast.

- `frontend/src/components/ui/button.tsx`
  - Add `glass` and `tinted` to `buttonVariants`.
  - Use `glass-pressable` and `glass-border` helpers; keep sizes unchanged.

- `frontend/src/components/ui/badge.tsx`
  - Add `glass` variant; introduce `variantColor?: 'success' | 'warn' | 'danger'` (optional) or use classes at call‑site with `.tint-*`.

- `frontend/src/components/ui/table.tsx`
  - Reduce row hover background to `hover:bg-transparent` or `hover:bg-white/4`.
  - Apply `.hairline` borders on rows and header.

- `frontend/src/components/ui/separator.tsx`
  - Add className default `hairline` in addition to `bg-border`.

- `frontend/src/App.tsx`
  - Header `h1` → add `className="text-liquid"`.
  - Refresh button → `variant="glass"`.
  - Stat cards → `<Card className="rounded-2xl" appearance="glass">`.
  - Server cards → same as above; apply glass badges with tints based on status.
  - Optional: Wrap stats section in a subtle tinted container if background is too contrasty.

---

**Status‑Tint Mapping (Server Badges)**
- `Connected` → `.tint-success` + `variant="glass"`.
- `Connecting` → `.tint-warn` + `variant="glass"`.
- `Disconnected` → `.tint-danger` + `variant="glass"`.

Implementation tip: Keep badge text `text-foreground` with 90–95% opacity; shapes provide the cue, not saturated text.

---

**Testing & QA Checklist**
- Verify text readability (WCAG AA) on both bright and dark regions of the wallpaper.
- Toggle OS dark mode (or app dark class) to validate tokens.
- Toggle `prefers-reduced-transparency` via DevTools emulate; ensure solid fallbacks.
- Resize window and ensure noise and borders remain crisp (no pixel seams at scaled DPRs).
- GPU profiling: ensure no full‑screen element carries `.glass`.

---

**Rollout Strategy**
1) Land utilities and tokens (`index.css`).
2) Add `Card`/`Button`/`Badge` variants behind non‑breaking defaults.
3) Convert stat + server cards and the refresh button.
4) Iterate on tints and heading intensity using screenshots from macOS to fine‑tune `--lg-text-*` and `--lg-border-*`.

---

**Appendix — Minimal Snippets**

1) Card `cva` (sketch)
```ts
// frontend/src/components/ui/card.tsx
import { cva, type VariantProps } from "class-variance-authority"
const cardVariants = cva(
  "relative rounded-2xl border",
  { variants: { appearance: {
      glass: "glass glass-border glass-noise",
      solid: "bg-card text-card-foreground shadow-sm",
    }}, defaultVariants: { appearance: "glass" } }
)
export function Card({ className, appearance, ...props }:
  React.ComponentProps<"div"> & VariantProps<typeof cardVariants>) {
  return <div className={cn(cardVariants({ appearance }), className)} {...props} />
}
```

2) Button `glass` variant (sketch)
```ts
// add inside buttonVariants.variants.variant:
glass: "relative glass glass-border glass-pressable border-transparent text-foreground/90",
tinted: "relative glass glass-border glass-pressable text-primary-foreground",
```

3) Heading style
```tsx
<h1 className="text-liquid text-3xl font-semibold tracking-tight">MCP Manager Dashboard</h1>
```

4) Using tints
```tsx
<Badge variant="glass" className="tint-success">Connected</Badge>
```

---

**What This Does Not Change (Yet)**
- No navigation/sidebar introduced; layout structure stays the same.
- No additional fonts bundled; relies on system SF. If you want Apple SF Text/Display weights pinned, we can add `-apple-system` stack (already default in most user agents) or ship fonts with appropriate licenses.

---

**Next Steps (after plan approval)**
- I can submit a focused PR that implements Phase 1 + Card/Button/Badge variants, then convert `App.tsx` usage. That will keep the diff readable and let us tune tokens live.
