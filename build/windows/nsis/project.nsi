Unicode true
!include "MUI2.nsh"
Name "IceTudio"
OutFile "..\..\..\bin\icetudio-installer.exe"
InstallDir "$LOCALAPPDATA\Programs\IceTudio"
RequestExecutionLevel user
!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
Section "IceTudio"
 SetOutPath "$INSTDIR"
 File "..\..\..\bin\icetudio.exe"
 File /oname=LICENSE "..\..\..\LICENSE"
 File /nonfatal "..\..\..\assets\soundfonts\MuseScore_General.sf3"
 SetOutPath "$INSTDIR\soundfont-notices"
 File "..\..\..\assets\soundfonts\*.md"
 File "..\..\..\assets\soundfonts\*.csv"
 SetOutPath "$INSTDIR\third-party\gridsound"
 File "..\..\..\frontend\public\vendor\gridsound\LICENSE"
 File "..\..\..\frontend\public\vendor\gridsound\NOTICE.md"
 SetOutPath "$INSTDIR\third-party\gs-utils"
 File "..\..\..\frontend\public\vendor\gridsound\gs-utils\LICENSE"
 SetOutPath "$INSTDIR\licenses"
 File /r "..\..\..\licenses\*"
 CreateShortcut "$SMPROGRAMS\IceTudio.lnk" "$INSTDIR\icetudio.exe"
 WriteUninstaller "$INSTDIR\uninstall.exe"
 WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\IceTudio" "DisplayName" "IceTudio"
 WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\IceTudio" "UninstallString" '$"$INSTDIR\uninstall.exe$"'
SectionEnd
Section "Uninstall"
 Delete "$INSTDIR\icetudio.exe"
 Delete "$INSTDIR\uninstall.exe"
 Delete "$SMPROGRAMS\IceTudio.lnk"
 DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\IceTudio"
 DetailPrint "Projects, settings, recovery data, fonts and notices have been retained."
 RMDir "$INSTDIR"
SectionEnd
