-- Seed default custom topbar links if not already configured.
-- Uses ON CONFLICT (key) DO NOTHING so admin edits or deliberate clearing ("[]") are preserved.
INSERT INTO settings (key, value)
VALUES (
    'custom_topbar_links',
    '[{"id":"canvas","label":"画布入口","url":"https://qimu.code1024.cc/","icon":"palette","icon_svg":"","visibility":"all","sort_order":0},{"id":"store","label":"商店入口","url":"https://ai.code1024.cc/","icon":"shoppingBag","icon_svg":"","visibility":"all","sort_order":1}]'
)
ON CONFLICT (key) DO NOTHING;
