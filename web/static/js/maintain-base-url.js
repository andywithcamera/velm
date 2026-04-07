// Prevent browser URL change
window.history.pushState({}, '', '/');
window.onpopstate = function() {
    window.history.pushState({}, '', '/');
};
