# Generated by go2rpm
%ifnarch s390x
%bcond_without check
%endif

%global goipath         pkg.deepin.io/lib
%global forgeurl        https://github.com/linuxdeepin/go-lib
Version:                5.6.0.4
%global tag             %{version}

%gometa

%global godevelheader %{expand:
Requires:       deepin-gir-generator
Requires:       dbus-x11
Requires:       iso-codes
Requires:       mobile-broadband-provider-info
Requires:       pkgconfig(gio-2.0)
Requires:       pkgconfig(gdk-3.0)
Requires:       pkgconfig(gdk-x11-3.0)
Requires:       pkgconfig(gdk-pixbuf-xlib-2.0)
Requires:       pkgconfig(libpulse)}

%global goname          golang-deepin-go-lib
%global godevelname     golang-deepin-go-lib-devel

%global common_description %{expand:
Deepin Golang library is a library containing many useful go routines for things
such as glib, gettext, archive, graphic,etc.}

%global golicenses      LICENSE
%global godocs          README.md

Name:           %{goname}
Release:        1%{?dist}
Summary:        Go bindings for Deepin Desktop Environment development

License:        GPLv3
URL:            %{gourl}
Source0:        %{gosource}

BuildRequires:  deepin-gir-generator
BuildRequires:  dbus-x11
BuildRequires:  iso-codes
BuildRequires:  mobile-broadband-provider-info
BuildRequires:  golang(github.com/cryptix/wav)
BuildRequires:  golang(github.com/linuxdeepin/go-x11-client)
BuildRequires:  golang(golang.org/x/image/bmp)
BuildRequires:  golang(golang.org/x/image/tiff)
BuildRequires:  golang(golang.org/x/net/context)
BuildRequires:  golang(gopkg.in/alecthomas/kingpin.v2)
BuildRequires:  golang(pkg.deepin.io/gir/gio-2.0)
BuildRequires:  golang(pkg.deepin.io/gir/glib-2.0)
BuildRequires:  golang(github.com/fsnotify/fsnotify)
BuildRequires:  golang(github.com/godbus/dbus)
BuildRequires:  golang(github.com/godbus/dbus/prop)
BuildRequires:  golang(github.com/godbus/dbus/introspect)
BuildRequires:  golang(github.com/mozillazg/go-pinyin)
BuildRequires:  pkgconfig(gio-2.0)
BuildRequires:  pkgconfig(gdk-3.0)
BuildRequires:  pkgconfig(gdk-x11-3.0)
BuildRequires:  pkgconfig(gdk-pixbuf-xlib-2.0)
BuildRequires:  pkgconfig(libpulse)

%if %{with check}
# Tests
BuildRequires:  golang(github.com/smartystreets/goconvey/convey)
BuildRequires:  golang(github.com/stretchr/testify/assert)
BuildRequires:  golang(gopkg.in/check.v1)
%endif

%description
%{common_description}

%gopkg

%prep
%goprep

%install
%gopkginstall

%if %{with check}
%check
%gocheck -d log -d procfs -d pulse -d shell -t users -d iso
%endif

%gopkgfiles

%changelog
