// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

class SearchApp {
    constructor() {
        this.article = null;
        this.isSearchResults = false;
        this.isArticle = false;
        
        this.initializeApp();
        this.bindEvents();
        this.setupSearchTypeBehavior();
    }

    setupSearchTypeBehavior() {
        const titleCheckbox = document.getElementById('titleSearch');
        const lexicalCheckbox = document.getElementById('lexicalSearch');
        
        if (titleCheckbox && lexicalCheckbox) {
            titleCheckbox.addEventListener('change', () => {
                if (titleCheckbox.checked) {
                    lexicalCheckbox.checked = false;
                }
            });
            
            lexicalCheckbox.addEventListener('change', () => {
                if (lexicalCheckbox.checked) {
                    titleCheckbox.checked = false;
                }
            });
        }
    }

    initializeApp() {
        this.searchInput = document.getElementById('searchInput');
        this.searchForm = document.getElementById('searchForm');
        this.resultsContainer = document.getElementById('resultsContainer');
        this.articleContent = document.getElementById('articleContent');
        this.loadingSpinner = document.getElementById('loadingSpinner');
        
        const urlParams = new URLSearchParams(window.location.search);
        this.language = urlParams.get('language') || 'en';
        this.ai = urlParams.get('ai') === 'true';
        
        this.configureAISearch();
    }

    configureAISearch() {
        if (!this.searchForm || !this.ai) return;
        
        const semanticSearchCheck = document.getElementById('semanticSearchCheck');
        const semanticSearch = document.getElementById('semanticSearch');
        
        if (this.ai && semanticSearch) {
            semanticSearch.checked = true;
        } else if (semanticSearchCheck) {
            semanticSearchCheck.classList.add('d-none');
        }
    }

    bindEvents() {
        if (this.searchForm) {
            this.searchForm.addEventListener('submit', (event) => {
                this.handleSearchSubmit(event);
            });
        }
        
        window.addEventListener('popstate', (event) => {
            if (this.isArticle) {
                this.showSearchResults();
            }
        });
    }

    async handleSearchSubmit(event) {
        event.preventDefault();
        
        this.clearPreviousResults();
        this.showResultsContainer();

        const query = this.searchInput.value.trim();
        const searchTypes = this.getSelectedSearchTypes();

        if (!query || searchTypes.length === 0) return;

        await this.executeSearches(query, searchTypes);
    }

    getSelectedSearchTypes() {
        return Array.from(document.querySelectorAll('input[name="searchType"]:checked'))
                   .map(el => el.value);
    }

    clearPreviousResults() {
        document.getElementById('articleTextContent').innerHTML = '';
        document.getElementById('articleTitle').textContent = '';
        
        const resultsList = this.resultsContainer.querySelector('ol');
        if (resultsList) resultsList.innerHTML = '';
        
        this.resultsContainer.querySelector('.alert')?.remove();
    }

    showResultsContainer() {
        this.resultsContainer.style.display = 'block';
        this.resultsContainer.classList.remove('d-none');
        this.articleContent.classList.add('d-none');
    }

    async executeSearches(query, searchTypes) {
        this.showLoadingSpinner();

        try {
            const searchPromises = searchTypes.map(type => 
                this.performSearch(query, type)
            );
            
            const allResults = await Promise.all(searchPromises);
            const flattenedResults = allResults.flat();
            
            this.handleSearchCompletion(query, flattenedResults);
        } catch (error) {
            console.error('Search execution error:', error);
            this.handleSearchCompletion(query, []);
        } finally {
            this.hideLoadingSpinner();
        }
    }

    async performSearch(query, type) {
        const endpoint = `/api/search/${type}`;
        const payload = {
            query: query,
            limit: parseInt(document.getElementById("limit").value) || 10
        };

        try {
            const response = await fetch(endpoint, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload),
            });
            
            const data = await response.json();
            
            if (data.status === 'success' && data.results) {
                this.displayResults(data.results, type);
                return data.results;
            }
            
            console.error('Search API error:', data.message);
            return [];
        } catch (error) {
            console.error('Network error:', error);
            return [];
        }
    }

    displayResults(results, type) {
        if (results.length === 0) return;

        this.isSearchResults = true;
        const container = this.resultsContainer.querySelector('ol');
        
        results.forEach(result => {
            const listItem = this.createResultListItem(result, type);
            container.appendChild(listItem);
        });
    }

    checkTitleSnippet(title, snippet) {
      return title == snippet.replace("<mark>","").replace("</mark>","")
    }

    createResultListItem(result, type) {
        const li = document.createElement('li');
        li.className = 'list-group-item d-flex justify-content-between align-items-start';

        const contentDiv = document.createElement('div');
        contentDiv.className = 'ms-2 me-auto';

        const titleLink = document.createElement('a');
        titleLink.href = '#';
        titleLink.className = 'text-decoration-none';
        titleLink.addEventListener('click', () => this.showArticle(result.article_id));
        if (type == "title" || this.checkTitleSnippet(result.title, result.snippet)) {
            titleLink.innerHTML = result.snippet;
        } else {
            titleLink.textContent = result.title;
        }

        const text = document.createElement('p');
        if (type == "distance" || type == "title" || this.checkTitleSnippet(result.title, result.snippet)) {
          text.textContent = result.text;
        } else {
          text.innerHTML = result.snippet;
        }

        contentDiv.appendChild(titleLink);
        contentDiv.appendChild(text);
        li.appendChild(contentDiv);

        return li;
    }

    handleSearchCompletion(query, allResults) {
        const totalResults = allResults.length;

        if (totalResults === 0) {
            this.showNoResultsMessage(query);
        } else if (totalResults === 1) {
            this.showArticle(allResults[0].article_id);
        }
    }

    showNoResultsMessage(query) {
        const noResults = document.createElement('div');
        noResults.className = 'alert alert-info';
        noResults.textContent = `No results found for "${query}"`;
        this.resultsContainer.appendChild(noResults);
    }

    async showArticle(articleId) {
        try {
            this.showLoadingSpinner();
            this.resultsContainer.classList.add('d-none');
            
            await this.fetchArticle(articleId);
            this.displayArticle();
        } catch (error) {
            console.error('Error showing article:', error);
        } finally {
            this.hideLoadingSpinner();
        }
    }

    async fetchArticle(articleId) {
        const response = await fetch(`/api/article?id=${articleId}`);
        const data = await response.json();
        
        if (data.status === 'success') {
            this.article = data.article;
        } else {
            throw new Error('Failed to fetch article');
        }
    }

    displayArticle() {
        if (!this.article) return;

        this.isArticle = true;
        this.isSearchResults = false;
        document.title = this.article.title;
        
        document.getElementById('articleTitle').textContent = this.article.title;
        this.renderArticleSections();
        
        document.getElementById('searchSection').classList.add('d-none');
        this.articleContent.classList.remove('d-none');
        
        window.history.pushState({ isArticle: true }, '');
    }

    showSearchResults() {
        this.isArticle = false;
        this.isSearchResults = true;
        document.getElementById('searchSection').classList.remove('d-none');
        this.articleContent.classList.add('d-none');
        this.resultsContainer.classList.remove('d-none');
        document.title = 'Search Results';
    }

    renderArticleSections() {
        const container = document.getElementById('articleTextContent');
        container.innerHTML = '';
        
        this.article.sections.forEach((section, index) => {
            const sectionElement = this.createSectionElement(section, index);
            container.appendChild(sectionElement);
        });
    }

    createSectionElement(section, index) {
        const sectionDiv = document.createElement('div');
        sectionDiv.className = 'mb-4';
        
        const title = document.createElement('h2');
        title.textContent = section.title;
        title.id = `section-${index}`;
        
        const content = document.createElement('p');
        content.textContent = section.content;
        content.id = `content-${index}`;
        
        sectionDiv.appendChild(title);
        sectionDiv.appendChild(content);
        
        return sectionDiv;
    }

    showLoadingSpinner() {
        if (this.loadingSpinner) {
            this.loadingSpinner.classList.remove('d-none');
        }
    }

    hideLoadingSpinner() {
        if (this.loadingSpinner) {
            this.loadingSpinner.classList.add('d-none');
        }
    }
}

document.addEventListener('DOMContentLoaded', () => {
    new SearchApp();
});
