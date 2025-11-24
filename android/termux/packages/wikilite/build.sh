TERMUX_PKG_HOMEPAGE=https://github.com/eja/wikilite
TERMUX_PKG_DESCRIPTION="Offline Lexical and Semantic Wikipedia Search"
TERMUX_PKG_LICENSE="GPL-3.0"
TERMUX_PKG_MAINTAINER="@termux"
TERMUX_PKG_VERSION="0.26.0"
TERMUX_PKG_SRCURL="git+https://github.com/eja/wikilite"
TERMUX_PKG_GIT_SUBMODULES=true
TERMUX_PKG_BUILD_IN_SRC=true

termux_step_pre_configure() {
	if [ "${TERMUX_ARCH}" = "arm" ]; then
		CFLAGS="${CFLAGS/-mfpu=neon/} -mfpu=vfp"
		CXXFLAGS="${CXXFLAGS/-mfpu=neon/} -mfpu=vfp"
	fi
}

termux_step_configure() {
  termux_setup_cmake
  termux_setup_golang

  mkdir build
  cd build
  cmake ..
}

termux_step_make() {
  cd build
  cmake --build .
}

termux_step_make_install() {
  install -Dm700 build/bin/wikilite "${TERMUX_PREFIX}/bin/wikilite"
}

