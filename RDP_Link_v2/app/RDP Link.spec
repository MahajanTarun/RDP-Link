# -*- mode: python ; coding: utf-8 -*-


a = Analysis(
    ['ubuntu_rdp_manager.py'],
    pathex=[],
    binaries=[],
    datas=[('receiver', 'receiver'), ('assets/rdp-link-tcc', 'assets')],
    hiddenimports=['office_viewer'],
    hookspath=[],
    hooksconfig={},
    runtime_hooks=[],
    excludes=['pytest', 'unittest', 'pydoc_data', 'test', 'numpy'],
    noarchive=False,
    optimize=0,
)
pyz = PYZ(a.pure)

exe = EXE(
    pyz,
    a.scripts,
    [],
    exclude_binaries=True,
    name='RDP Link',
    debug=False,
    bootloader_ignore_signals=False,
    strip=False,
    upx=True,
    console=False,
    disable_windowed_traceback=False,
    argv_emulation=False,
    target_arch=None,
    codesign_identity=None,
    entitlements_file=None,
    icon=['assets/icon.icns'],
)
coll = COLLECT(
    exe,
    a.binaries,
    a.datas,
    strip=False,
    upx=True,
    upx_exclude=[],
    name='RDP Link',
)
app = BUNDLE(
    coll,
    name='RDP Link.app',
    icon='assets/icon.icns',
    bundle_identifier='com.eminentailabs.rdplink',
)
